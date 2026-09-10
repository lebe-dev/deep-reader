// Package comments turns a discussion-thread URL into readable article content.
// It satisfies the ports.Extractor interface by routing: a URL a registered
// [Source] recognises is fetched from that site's API and rendered as Markdown,
// anything else is handed to the ordinary article extractor.
//
// A thread page is worthless to a generic HTML extractor — readability sees a
// table of nested rows and returns navigation chrome, and markdown.new spends a
// budget unit to produce the same thing — so a comment source never falls back
// to the article path: a failure there is a fetch failure the enrichment pool
// retries, not an invitation to store a scraped page as if it were the thread.
//
// Adding a site means implementing [Source] in this package and registering it
// in cmd/server; nothing else in the pipeline changes.
package comments

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"

	"deep-reader/internal/ports"
)

// Source is one discussion site that can be read through its own API.
// Implementations live in this package (see hackerNews).
type Source interface {
	// Name identifies the source in logs.
	Name() string
	// Match reports whether u is a thread URL this source knows how to fetch.
	Match(u *url.URL) bool
	// Fetch pulls the thread behind u and renders it as Markdown, returning a
	// result stamped with model.SourceTypeComments.
	Fetch(ctx context.Context, u *url.URL) (*ports.ExtractResult, error)
}

// Router dispatches an extraction between the registered comment sources and
// the ordinary article extractor. It implements ports.Extractor. Construct via
// [NewRouter].
type Router struct {
	sources  []Source
	fallback ports.Extractor
}

// NewRouter builds a Router. fallback is the article extractor used for every
// URL no source claims; sources are tried in registration order.
func NewRouter(fallback ports.Extractor, sources ...Source) *Router {
	return &Router{sources: sources, fallback: fallback}
}

// Extract routes rawURL: the first source whose Match accepts it fetches the
// thread, otherwise the article extractor runs. A malformed URL goes to the
// fallback, which owns URL validation and its error messages.
func (r *Router) Extract(ctx context.Context, rawURL string) (*ports.ExtractResult, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return r.fallback.Extract(ctx, rawURL)
	}

	for _, src := range r.sources {
		if !src.Match(u) {
			continue
		}
		slog.Info("comments: routing to comment source", "url", rawURL, "source", src.Name())
		result, ferr := src.Fetch(ctx, u)
		if ferr != nil {
			return nil, fmt.Errorf("comments: %s: %w", src.Name(), ferr)
		}
		return result, nil
	}

	return r.fallback.Extract(ctx, rawURL)
}
