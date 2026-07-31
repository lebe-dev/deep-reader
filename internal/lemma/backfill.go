package lemma

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"deep-reader/internal/model"
	"deep-reader/internal/ports"
)

// Backfill tuning. Articles ingested before the lemmatizer existed have
// lemma-less tokens; a one-shot background pass repairs them (WORD-CACHE-ARCH.md
// §4.5). It is deliberately unhurried: correctness comes from resumability, not
// from finishing fast, and it must never starve the enrichment workers.
const (
	// backfillBatch is how many articles one pass loads at a time.
	backfillBatch = 20
	// backfillPause is the idle gap between batches — the rate limit that keeps
	// the single SQLite writer available to the pipeline.
	backfillPause = 250 * time.Millisecond
)

// BackfillStore is the slice of ports.Store the backfill needs. Narrowing it
// keeps the pass testable without a full store double; *store.SQLite and
// ports.Store both satisfy it.
type BackfillStore interface {
	ListArticlesForLemmaBackfill(ctx context.Context, version, limit int) ([]model.Article, error)
	SaveTokenLemmas(ctx context.Context, id string, tokens []model.Token, version int) error
}

// RunBackfill annotates every article whose stored tokens predate the current
// lemmatizer generation, in batches, until none are left or ctx is cancelled.
// Run it in its own goroutine at startup.
//
// It is idempotent and resumable: each batch stamps lemma_version, so a restart
// mid-pass picks up exactly where it stopped and a completed pass is a no-op.
// It only ADDS Lemma to existing tokens — it never re-tokenizes, because every
// enrichment reference (token_index, start_index, end_index) indexes into that
// exact slice and a shifted index would silently corrupt every annotation in
// the library.
//
// Expect one visible consequence: the writes bump articles.updated_at, so the
// first sync after upgrading re-downloads every enriched payload, once.
func (l *Lemmatizer) RunBackfill(ctx context.Context, st BackfillStore, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}

	started := time.Now()
	processed := 0
	for {
		if ctx.Err() != nil {
			return
		}

		articles, err := st.ListArticlesForLemmaBackfill(ctx, CurrentVersion, backfillBatch)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return
			}
			log.Error("lemma backfill: listing articles failed, giving up for this boot",
				slog.Any("error", err), slog.Int("processed", processed))
			return
		}
		if len(articles) == 0 {
			if processed > 0 {
				log.Info("lemma backfill: complete",
					slog.Int("articles", processed),
					slog.Duration("took", time.Since(started)))
			}
			return
		}

		advanced := false
		for i := range articles {
			if ctx.Err() != nil {
				return
			}
			a := &articles[i]
			if err := st.SaveTokenLemmas(ctx, a.ID, l.Annotate(a.Tokens), CurrentVersion); err != nil {
				if ctx.Err() != nil {
					return
				}
				// A deleted article is expected (ErrNotFound); anything else is
				// logged and skipped so one bad row cannot stall the pass. The
				// article keeps its old lemma_version and is retried next boot.
				if !errors.Is(err, ports.ErrNotFound) {
					log.Warn("lemma backfill: article skipped",
						slog.String("article_id", a.ID), slog.Any("error", err))
				}
				continue
			}
			advanced = true
			processed++
		}

		// A batch where nothing was stamped would come back identical forever,
		// because selection is driven by lemma_version. Stop instead of
		// spinning; the next boot retries the same rows.
		if !advanced {
			log.Warn("lemma backfill: no article in the batch could be stamped, stopping",
				slog.Int("batch", len(articles)), slog.Int("processed", processed))
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backfillPause):
		}
	}
}
