// Package markdown integrates the markdown.new service as Deep Reader's primary
// content extractor. markdown.new converts a public URL into clean Markdown
// (optimised for LLM consumption); [Client] calls it and adapts the result to
// [ports.ExtractResult], while [Chain] layers a daily request-unit budget and a
// readability fallback on top.
//
// # Service contract (observed)
//
// POST https://markdown.new/ with JSON {"url", "method", "retain_images"} and
// Content-Type: application/json returns JSON:
//
//	{"success": true, "url": "...", "title": "...", "content": "<markdown>",
//	 "method": "...", "duration_ms": 123, "tokens": 42}
//
// The free plan grants 500 request units/day per IP and answers HTTP 429 when
// exhausted. The [Chain] budget mirrors that limit locally so the UI can warn
// before the service starts rejecting requests.
package markdown

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	nurl "net/url"
	"strings"
	"time"

	"deep-reader/internal/config"
	"deep-reader/internal/ports"
)

// Sentinel errors returned by Client.Extract. Callers match them with
// errors.Is.
var (
	// ErrConversionFailed is returned when markdown.new responds with a non-2xx
	// status or success=false.
	ErrConversionFailed = errors.New("markdown.new conversion failed")
	// ErrEmptyContent is returned when markdown.new yields no usable text after
	// markdown is stripped to plain text.
	ErrEmptyContent = errors.New("markdown.new returned empty content")
	// ErrRateLimited is returned (wrapping ErrConversionFailed) when markdown.new
	// answers HTTP 429 — the service's own daily budget is exhausted.
	ErrRateLimited = errors.New("markdown.new rate limit reached")
)

// userAgent identifies Deep Reader to markdown.new.
const userAgent = "Mozilla/5.0 (compatible; DeepReader/1.0; +https://github.com/deep-reader)"

// Client calls the markdown.new conversion endpoint. Construct it with New. It
// implements ports.Extractor.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New creates a markdown.new client from cfg.
func New(cfg *config.Config) *Client {
	timeout := cfg.MarkdownTimeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	return &Client{
		baseURL:    strings.TrimRight(cfg.MarkdownBaseURL, "/"),
		httpClient: &http.Client{Timeout: timeout},
	}
}

// apiRequest is the markdown.new POST body.
type apiRequest struct {
	URL          string `json:"url"`
	Method       string `json:"method"`
	RetainImages bool   `json:"retain_images"`
}

// apiResponse is the markdown.new JSON response.
type apiResponse struct {
	Success    bool   `json:"success"`
	URL        string `json:"url"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	Method     string `json:"method"`
	DurationMs int    `json:"duration_ms"`
	Error      string `json:"error"`
}

// Extract sends rawURL to markdown.new, converts the returned Markdown to plain
// text suitable for the deterministic tokenizer, and adapts it to
// ports.ExtractResult. It implements ports.Extractor.
//
// markdown.new performs the fetch itself (it is an external service), so there
// is no SSRF concern on this path; only the scheme is validated. The readability
// fallback in Chain retains the full private-range guard.
func (c *Client) Extract(ctx context.Context, rawURL string) (*ports.ExtractResult, error) {
	if err := validateScheme(rawURL); err != nil {
		return nil, err
	}

	reqBody, err := json.Marshal(apiRequest{URL: rawURL, Method: "auto"})
	if err != nil {
		return nil, fmt.Errorf("markdown: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("markdown: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", userAgent)

	slog.Debug("markdown: converting URL", "url", rawURL, "base_url", c.baseURL)
	start := time.Now()
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("markdown: request %q: %w", rawURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("markdown: read response: %w", err)
	}

	// markdown.new surfaces remaining daily requests in this header when present.
	remaining := resp.Header.Get("x-rate-limit-remaining")
	slog.Debug("markdown: response received",
		"url", rawURL,
		"status", resp.StatusCode,
		"body_bytes", len(body),
		"rate_limit_remaining", remaining,
		"duration_ms", time.Since(start).Milliseconds(),
	)

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("%w: %w", ErrRateLimited, ErrConversionFailed)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: HTTP %d", ErrConversionFailed, resp.StatusCode)
	}

	var out apiResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("markdown: unmarshal response: %w", err)
	}
	if !out.Success {
		msg := out.Error
		if msg == "" {
			msg = "service reported failure"
		}
		return nil, fmt.Errorf("%w: %s", ErrConversionFailed, msg)
	}

	text := markdownToText(out.Content)
	if strings.TrimSpace(text) == "" {
		return nil, ErrEmptyContent
	}

	canonical := out.URL
	if canonical == "" {
		canonical = rawURL
	}

	return &ports.ExtractResult{
		CanonicalURL: canonical,
		Title:        strings.TrimSpace(out.Title),
		Author:       "", // markdown.new does not return byline metadata
		Domain:       hostOf(canonical),
		Lang:         "", // markdown.new does not return a language code
		HTML:         out.Content,
		Text:         text,
	}, nil
}

// validateScheme rejects empty URLs and non-http(s) schemes before any network
// traffic, mirroring the extract package's contract.
func validateScheme(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("markdown: empty URL")
	}
	u, err := nurl.ParseRequestURI(rawURL)
	if err != nil {
		return fmt.Errorf("markdown: invalid URL %q: %w", rawURL, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return nil
	default:
		return fmt.Errorf("markdown: unsupported scheme %q (only http/https allowed)", u.Scheme)
	}
}

// hostOf returns the hostname of rawURL, or "" if it cannot be parsed.
func hostOf(rawURL string) string {
	u, err := nurl.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
