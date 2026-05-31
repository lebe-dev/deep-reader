package markdown

import (
	"context"
	"log/slog"

	"deep-reader/internal/config"
	"deep-reader/internal/ports"
)

// Budget is the persistence the Chain needs to enforce the markdown.new daily
// request-unit budget. *store.SQLite satisfies it.
type Budget interface {
	// TryConsumeMarkdownUnits reserves cost units for today if dailyLimit allows.
	TryConsumeMarkdownUnits(ctx context.Context, cost, dailyLimit int) (allowed bool, usedAfter int, err error)
	// RefundMarkdownUnits returns reserved units when the conversion did not yield
	// usable content.
	RefundMarkdownUnits(ctx context.Context, cost int) error
}

// Chain is the composite extractor: markdown.new is the primary source, with a
// local readability extractor as fallback. It implements ports.Extractor.
//
// For each Extract it first reserves budget. If the budget is available it calls
// the primary; on success the units stay spent, on failure they are refunded and
// it falls back. When the budget is exhausted it skips the primary entirely and
// goes straight to the fallback, so adding articles never hard-fails — it just
// degrades to local extraction until the daily budget resets.
type Chain struct {
	primary    ports.Extractor
	fallback   ports.Extractor
	budget     Budget
	cost       int
	dailyLimit int
}

// NewChain builds a Chain. primary is the markdown.new client, fallback is the
// readability extractor, budget is the persistence (store), and cfg supplies the
// per-article cost and daily limit.
func NewChain(primary, fallback ports.Extractor, budget Budget, cfg *config.Config) *Chain {
	return &Chain{
		primary:    primary,
		fallback:   fallback,
		budget:     budget,
		cost:       cfg.MarkdownCostPerArticle,
		dailyLimit: cfg.MarkdownDailyLimit,
	}
}

// Extract runs the markdown.new → readability chain described on Chain.
func (c *Chain) Extract(ctx context.Context, rawURL string) (*ports.ExtractResult, error) {
	allowed, usedAfter, err := c.budget.TryConsumeMarkdownUnits(ctx, c.cost, c.dailyLimit)
	if err != nil {
		// A budget bookkeeping error must not block ingestion; skip the primary.
		slog.Warn("markdown: budget check failed, using readability fallback", "url", rawURL, "err", err)
		return c.fallback.Extract(ctx, rawURL)
	}

	if !allowed {
		slog.Info("markdown: daily budget exhausted, using readability fallback",
			"url", rawURL, "daily_limit", c.dailyLimit)
		return c.fallback.Extract(ctx, rawURL)
	}

	result, perr := c.primary.Extract(ctx, rawURL)
	if perr == nil {
		slog.Info("markdown: extracted via markdown.new",
			"url", rawURL, "units_used_today", usedAfter, "daily_limit", c.dailyLimit)
		return result, nil
	}

	// The primary did not produce usable content — refund and fall back so the
	// units are not wasted on a failed conversion.
	if rerr := c.budget.RefundMarkdownUnits(ctx, c.cost); rerr != nil {
		slog.Warn("markdown: failed to refund units after primary failure", "url", rawURL, "err", rerr)
	}
	slog.Warn("markdown: primary extraction failed, falling back to readability", "url", rawURL, "err", perr)
	return c.fallback.Extract(ctx, rawURL)
}
