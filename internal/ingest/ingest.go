// Package ingest implements the article ingestion pipeline for Deep Reader.
// It satisfies the [ports.Ingestor] interface. Construct via [New].
//
// The pipeline executed by [Ingestor.Add] is:
//  1. Normalize the raw URL (lowercase host, strip utm_* params, strip fragment).
//  2. Compute url_hash = sha256 hex of the normalized URL.
//  3. Dedup: if an article with that hash exists and its enrichment_version
//     matches cfg.EnrichmentVersion, return the existing article immediately.
//  4. Persist as a new article with status=queued (no content yet).
//  5. Notify the enrichment worker, which performs the fetch and enrich stages
//     asynchronously.
//  6. Return the newly-created article (without waiting for fetch/enrichment).
package ingest

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"deep-reader/internal/config"
	"deep-reader/internal/model"
	"deep-reader/internal/ports"
	"deep-reader/internal/tokenize"
)

// Ingestor orchestrates the article ingestion pipeline. Use [New] to construct.
type Ingestor struct {
	cfg    *config.Config
	store  ports.Store
	worker ports.EnrichmentWorker
}

// New constructs an Ingestor. All arguments are required and must be non-nil.
func New(cfg *config.Config, st ports.Store, worker ports.EnrichmentWorker) *Ingestor {
	return &Ingestor{
		cfg:    cfg,
		store:  st,
		worker: worker,
	}
}

// Add ingests rawURL. It normalises the URL, deduplicates by hash, persists a
// new article in status=queued, notifies the worker (which fetches and enriches
// asynchronously), and returns the article.
//
// Add does not fetch or extract: fetch failures surface as the fetch_failed
// stage on the persisted article, not as an error here. Only URL-normalisation
// failures (which must not create a record) are returned as errors.
//
// If an article with the same url_hash already exists and its
// enrichment_version equals cfg.EnrichmentVersion the existing article is
// returned without re-fetching or calling the LLM.
func (ing *Ingestor) Add(ctx context.Context, rawURL string) (*model.Article, error) {
	slog.Debug("ingest: add requested", "url", rawURL)

	normalized, err := NormalizeURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("ingest: normalize URL: %w", err)
	}

	hash := URLHash(normalized)
	slog.Debug("ingest: url normalized", "url", rawURL, "normalized", normalized, "url_hash", hash)

	// Dedup: return the existing article if it is already at the current
	// enrichment version (no re-fetch, no LLM spend).
	existing, err := ing.store.GetArticleByHash(ctx, hash)
	if err == nil {
		// Article found.
		if existing.EnrichmentVersion == ing.cfg.EnrichmentVersion {
			slog.Info("ingest: returning cached article (dedup hit)",
				"article_id", existing.ID,
				"url_hash", hash,
				"enrichment_version", existing.EnrichmentVersion,
			)
			return existing, nil
		}
		// Version mismatch — fall through and re-ingest.
		slog.Info("ingest: enrichment version mismatch, re-ingesting",
			"article_id", existing.ID,
			"url_hash", hash,
			"stored_version", existing.EnrichmentVersion,
			"current_version", ing.cfg.EnrichmentVersion,
		)
	} else if !isNotFound(err) {
		return nil, fmt.Errorf("ingest: dedup lookup: %w", err)
	}

	// Persist a queued record. The worker fills in title/content/tokens during
	// the fetch stage and the enrichment during the enrich stage. SourceURL is
	// seeded with the normalized URL and replaced with the canonical URL after
	// fetch; SourceDomain is derived so the library card has something to show
	// before the fetch completes.
	now := time.Now().UTC()
	article := &model.Article{
		ID:                ulid.Make().String(),
		SourceURL:         normalized,
		URLHash:           hash,
		SourceDomain:      hostOf(normalized),
		Status:            model.StatusQueued,
		EnrichmentVersion: ing.cfg.EnrichmentVersion,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if err := ing.store.CreateArticle(ctx, article); err != nil {
		if isErr(err, ports.ErrDuplicate) {
			// Race condition: another request inserted the same hash between our
			// dedup check and the insert. Fetch and return the winner.
			slog.Debug("ingest: duplicate insert race, returning existing article", "url_hash", hash)
			got, lookupErr := ing.store.GetArticleByHash(ctx, hash)
			if lookupErr != nil {
				return nil, fmt.Errorf("ingest: post-duplicate lookup: %w", lookupErr)
			}
			return got, nil
		}
		return nil, fmt.Errorf("ingest: create article: %w", err)
	}

	slog.Info("ingest: article added",
		"article_id", article.ID,
		"source_url", article.SourceURL,
		"domain", article.SourceDomain,
		"status", article.Status,
	)

	ing.worker.Notify()

	return article, nil
}

// AddText ingests pasted raw text directly, bypassing the fetch/extract stage.
// It tokenizes the text, persists the article already in status=fetched (with
// its content saved to the database), and notifies the worker, which runs the
// enrich stage asynchronously. title is optional; when empty a heading is
// derived from the first line of text.
//
// Dedup is by a SHA-256 of the text (prefixed so it cannot collide with a URL
// hash). A matching article at the current enrichment version is returned as-is.
func (ing *Ingestor) AddText(ctx context.Context, title, text string) (*model.Article, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("ingest: empty text")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = deriveTitle(text)
	}

	hash := URLHash("text:" + text)
	slog.Debug("ingest: add text requested", "url_hash", hash, "bytes", len(text))

	// Dedup against an already-ingested identical paste.
	existing, err := ing.store.GetArticleByHash(ctx, hash)
	if err == nil {
		if existing.EnrichmentVersion == ing.cfg.EnrichmentVersion {
			slog.Info("ingest: returning cached text article (dedup hit)", "article_id", existing.ID, "url_hash", hash)
			return existing, nil
		}
		slog.Info("ingest: enrichment version mismatch for text, re-ingesting", "article_id", existing.ID, "url_hash", hash)
	} else if !isNotFound(err) {
		return nil, fmt.Errorf("ingest: dedup lookup: %w", err)
	}

	now := time.Now().UTC()
	article := &model.Article{
		ID:                ulid.Make().String(),
		URLHash:           hash,
		Title:             title,
		Status:            model.StatusFetched,
		OriginalText:      text,
		Tokens:            tokenize.Tokenize(text),
		EnrichmentVersion: ing.cfg.EnrichmentVersion,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if err := ing.store.CreateArticle(ctx, article); err != nil {
		if isErr(err, ports.ErrDuplicate) {
			slog.Debug("ingest: duplicate text insert race, returning existing article", "url_hash", hash)
			got, lookupErr := ing.store.GetArticleByHash(ctx, hash)
			if lookupErr != nil {
				return nil, fmt.Errorf("ingest: post-duplicate lookup: %w", lookupErr)
			}
			return got, nil
		}
		return nil, fmt.Errorf("ingest: create text article: %w", err)
	}

	slog.Info("ingest: text article added", "article_id", article.ID, "title", article.Title, "status", article.Status)

	ing.worker.Notify()

	return article, nil
}

// deriveTitle builds a short heading from the first non-empty line of text,
// truncated to a reasonable length. Returns "Untitled" if nothing usable.
func deriveTitle(text string) string {
	const maxLen = 120
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if r := []rune(line); len(r) > maxLen {
			return strings.TrimSpace(string(r[:maxLen])) + "…"
		}
		return line
	}
	return "Untitled"
}

// Retry resumes a failed article from the stage that failed and notifies the
// worker. Returns [ports.ErrNotFound] for an unknown id.
func (ing *Ingestor) Retry(ctx context.Context, id string) error {
	if err := ing.store.RetryArticle(ctx, id); err != nil {
		return err // preserve ErrNotFound
	}
	slog.Info("ingest: article queued for retry", "article_id", id)
	ing.worker.Notify()
	return nil
}

// ReEnrich re-runs enrichment for an already-enriched article and notifies the
// worker. mode "full" re-translates the whole article from scratch (keeping the
// fetched content), mode "topup" only fills the spans left uncovered by the
// current sentence translations. Returns [ports.ErrNotFound] for an unknown id.
func (ing *Ingestor) ReEnrich(ctx context.Context, id, mode string) error {
	if err := ing.store.ReEnrich(ctx, id, mode); err != nil {
		return err // preserve ErrNotFound
	}
	slog.Info("ingest: article queued for re-enrich", "article_id", id, "mode", mode)
	ing.worker.Notify()
	return nil
}

// hostOf returns the host of a normalized URL, or "" if it cannot be parsed.
// Used to seed SourceDomain before the fetch stage resolves the real domain.
func hostOf(normalized string) string {
	u, err := url.Parse(normalized)
	if err != nil {
		return ""
	}
	return u.Host
}

// NormalizeURL returns the canonical form of rawURL used for deduplication:
//   - scheme and host are lowercased
//   - utm_* query parameters are stripped
//   - the fragment (#…) is removed
//
// An error is returned if rawURL cannot be parsed or has no host.
func NormalizeURL(rawURL string) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("empty URL")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("URL has no host: %q", rawURL)
	}

	// Lowercase scheme and host.
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)

	// Strip fragment.
	u.Fragment = ""
	u.RawFragment = ""

	// Strip utm_* tracking parameters.
	if u.RawQuery != "" {
		q := u.Query()
		for key := range q {
			if strings.HasPrefix(strings.ToLower(key), "utm_") {
				delete(q, key)
			}
		}
		u.RawQuery = q.Encode()
	}

	return u.String(), nil
}

// URLHash returns the hex-encoded SHA-256 of the normalised URL string.
// This is the value stored in articles.url_hash.
func URLHash(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("%x", sum)
}

// isNotFound reports whether err wraps ports.ErrNotFound.
func isNotFound(err error) bool {
	return errors.Is(err, ports.ErrNotFound)
}

// isErr reports whether err wraps target using errors.Is semantics.
func isErr(err, target error) bool {
	return errors.Is(err, target)
}
