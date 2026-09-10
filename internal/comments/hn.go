package comments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"deep-reader/internal/config"
	"deep-reader/internal/model"
	"deep-reader/internal/ports"
)

// Sentinel errors returned by the Hacker News source. Callers match them with
// errors.Is.
var (
	// ErrEmptyThread is returned when the item exists but carries nothing to
	// read: a story with no text and no comments yet. It is terminal — the user
	// can retry the article once the discussion has started.
	ErrEmptyThread = errors.New("thread has no comments yet")
	// ErrUnknownItem is returned when the API does not know the item id at all
	// after the retries are exhausted (a mistyped or deleted item).
	ErrUnknownItem = errors.New("unknown thread")
)

// TemporaryError marks a failure the enrichment pool should retry with backoff:
// a transport fault, a 5xx, a rate-limit, or — importantly — a 404 on a thread
// posted minutes ago, which the search API has not indexed yet. Without the
// retry those brand-new front-page threads, the ones most likely to be shared,
// would land as fetch_failed on the first attempt.
type TemporaryError struct{ Err error }

func (e *TemporaryError) Error() string { return e.Err.Error() }

func (e *TemporaryError) Unwrap() error { return e.Err }

// Retryable tells the enrichment pool to retry the fetch stage.
func (e *TemporaryError) Retryable() bool { return true }

// hnAPIBaseURL is the Algolia HN search API. It returns a whole thread — story
// plus every comment, nested — in a single request, which is why it is used
// instead of the official Firebase API where the same thread costs one request
// per comment.
const hnAPIBaseURL = "https://hn.algolia.com/api/v1"

// hnHost is the only host whose item pages this source claims. HN has no other
// domain, so the match stays an exact comparison (plus the www. prefix).
const hnHost = "news.ycombinator.com"

// hnMaxBodyBytes caps the API response read at 32 MiB. The largest HN threads
// serialize to a few megabytes; the cap only stops a pathological response from
// exhausting memory.
const hnMaxBodyBytes = 32 * 1024 * 1024

// hackerNews reads Hacker News threads. Construct via [NewHackerNews].
type hackerNews struct {
	client  *http.Client
	baseURL string
}

// NewHackerNews builds the Hacker News comment source. It reuses the extraction
// timeout (READABILITY_TIMEOUT) rather than introducing a knob of its own: a
// thread fetch is one HTTP request in the same fetch stage.
func NewHackerNews(cfg *config.Config) Source {
	timeout := cfg.ReadabilityTimeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &hackerNews{
		client:  &http.Client{Timeout: timeout},
		baseURL: hnAPIBaseURL,
	}
}

// Name implements Source.
func (h *hackerNews) Name() string { return "hacker news" }

// Match implements Source: it accepts the item permalink
// https://news.ycombinator.com/item?id=<numeric id>, which is the link the
// "comments" line on the front page points to.
func (h *hackerNews) Match(u *url.URL) bool {
	if u == nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	if host != hnHost {
		return false
	}
	if strings.Trim(strings.ToLower(u.Path), "/") != "item" {
		return false
	}
	return hnItemID(u) != ""
}

// hnItemID returns the numeric id query parameter of an item URL, or "".
func hnItemID(u *url.URL) string {
	id := u.Query().Get("id")
	if id == "" {
		return ""
	}
	if _, err := strconv.ParseUint(id, 10, 64); err != nil {
		return ""
	}
	return id
}

// Fetch implements Source: it pulls the whole thread in one API call and
// renders it as Markdown.
func (h *hackerNews) Fetch(ctx context.Context, u *url.URL) (*ports.ExtractResult, error) {
	id := hnItemID(u)
	if id == "" {
		return nil, fmt.Errorf("not an item URL: %s", u.String())
	}

	item, err := h.fetchItem(ctx, id)
	if err != nil {
		return nil, err
	}

	text, count := renderThread(item)
	if strings.TrimSpace(text) == "" {
		return nil, ErrEmptyThread
	}

	slog.Info("comments: hacker news thread rendered",
		"item_id", id, "comment_count", count, "text_bytes", len(text))

	return &ports.ExtractResult{
		CanonicalURL:  "https://" + hnHost + "/item?id=" + id,
		Title:         threadTitle(item),
		Author:        item.Author,
		Domain:        hnHost,
		Lang:          "en",
		Text:          text,
		SourceType:    model.SourceTypeComments,
		ContentFormat: model.ContentFormatMarkdown,
	}, nil
}

// fetchItem GETs one item (story or comment) with its whole child tree.
func (h *hackerNews) fetchItem(ctx context.Context, id string) (*hnItem, error) {
	endpoint := h.baseURL + "/items/" + id
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, &TemporaryError{Err: fmt.Errorf("fetch item %s: %w", id, err)}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// A 404 usually means "not indexed yet" rather than "does not exist", so
		// it is retried like a 5xx; after the retries are exhausted it surfaces as
		// ErrUnknownItem so the library card explains what happened.
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, &TemporaryError{Err: fmt.Errorf("%w: item %s: HTTP %d", ErrUnknownItem, id, resp.StatusCode)}
		}
		return nil, fmt.Errorf("item %s: HTTP %d", id, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, hnMaxBodyBytes))
	if err != nil {
		return nil, &TemporaryError{Err: fmt.Errorf("read item %s: %w", id, err)}
	}

	var item hnItem
	if err := json.Unmarshal(body, &item); err != nil {
		return nil, fmt.Errorf("decode item %s: %w", id, err)
	}
	return &item, nil
}

// hnItem is one node of the thread as the Algolia API returns it. Fields the
// renderer does not use are omitted. A deleted comment arrives with author and
// text null, which decodes into empty strings — the renderer skips such a node
// while keeping its replies (see renderThread).
type hnItem struct {
	ID       int64     `json:"id"`
	Type     string    `json:"type"`
	Author   string    `json:"author"`
	Title    string    `json:"title"`
	URL      string    `json:"url"`
	Text     string    `json:"text"`
	Points   int       `json:"points"`
	Children []*hnItem `json:"children"`
}
