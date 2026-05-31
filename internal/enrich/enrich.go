// Package enrich implements the enrichment worker pool that drains pending
// articles from the store, calls the LLM client, validates the returned
// enrichment, and persists the result.
//
// Constructor: NewPool(cfg, store, llm) *Pool — satisfies ports.EnrichmentWorker.
package enrich

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"deep-reader/internal/config"
	"deep-reader/internal/model"
	"deep-reader/internal/ports"
)

const (
	// pollInterval is the safety-net periodic wakeup interval so that articles
	// that were queued before Start was called (or whose Notify was lost) are
	// eventually processed.
	pollInterval = 30 * time.Second

	// batchSize is the number of pending articles fetched per drain iteration.
	// Each worker fetches its own batch independently.
	batchSize = 10

	// baseBackoff is the initial retry wait before exponential growth.
	baseBackoff = 2 * time.Second

	// maxBackoff caps the per-attempt wait regardless of cfg.LLMMaxRetries.
	maxBackoff = 60 * time.Second
)

// Pool is a fixed-size worker pool that enriches pending articles.
// Construct it with NewPool; run Start in its own goroutine.
type Pool struct {
	cfg    *config.Config
	store  ports.Store
	llm    ports.LLMClient
	notify chan struct{}
}

// NewPool creates a new Pool. It satisfies ports.EnrichmentWorker via *Pool.
// Start(ctx) must be called to launch the workers.
func NewPool(cfg *config.Config, st ports.Store, client ports.LLMClient) *Pool {
	return &Pool{
		cfg:    cfg,
		store:  st,
		llm:    client,
		notify: make(chan struct{}, 1),
	}
}

// Notify signals that new pending articles may be available. Non-blocking; a
// second call before the first is consumed is a no-op (coalesced).
func (p *Pool) Notify() {
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

// Start launches cfg.LLMMaxConcurrent workers and blocks until ctx is
// cancelled. Run this in its own goroutine.
func (p *Pool) Start(ctx context.Context) {
	n := p.cfg.LLMMaxConcurrent
	if n < 1 {
		n = 1
	}

	slog.Info("enrich: pool starting", "workers", n)

	// Send an initial notification so leftover pending articles from before
	// Start are processed immediately.
	p.Notify()

	// Each worker has its own goroutine; they share the same notify channel.
	done := make(chan struct{})
	for i := 0; i < n; i++ {
		go func(workerID int) {
			defer func() { done <- struct{}{} }()
			p.runWorker(ctx, workerID)
		}(i)
	}

	// Wait for all workers to finish.
	for i := 0; i < n; i++ {
		<-done
	}
	slog.Info("enrich: pool stopped")
}

// runWorker is the per-goroutine loop. It wakes on p.notify or on the periodic
// poll ticker, drains all available pending articles, then sleeps again.
func (p *Pool) runWorker(ctx context.Context, workerID int) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.drain(ctx, workerID)
		case <-p.notify:
			p.drain(ctx, workerID)
		}
	}
}

// drain fetches and processes pending articles until none remain (or ctx is
// cancelled).
func (p *Pool) drain(ctx context.Context, workerID int) {
	for {
		if ctx.Err() != nil {
			return
		}

		articles, err := p.store.ListPending(ctx, batchSize)
		if err != nil {
			slog.Error("enrich: list pending failed", "worker", workerID, "err", err)
			return
		}
		if len(articles) == 0 {
			return
		}

		for i := range articles {
			if ctx.Err() != nil {
				return
			}
			p.processArticle(ctx, workerID, &articles[i])
		}

		// If we got a full batch there may be more; loop back to check.
		if len(articles) < batchSize {
			return
		}
	}
}

// processArticle enriches a single article with exponential back-off retries.
// On terminal failure it marks the article as failed.
func (p *Pool) processArticle(ctx context.Context, workerID int, a *model.Article) {
	log := slog.With("worker", workerID, "article_id", a.ID)
	maxRetries := p.cfg.LLMMaxRetries

	settings, err := p.store.GetSettings(ctx)
	if err != nil {
		log.Error("enrich: get settings failed", "err", err)
		return
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if ctx.Err() != nil {
			return
		}

		if attempt > 0 {
			wait := backoffDuration(attempt, baseBackoff, maxBackoff)
			log.Info("enrich: backing off before retry",
				"attempt", attempt,
				"wait_ms", wait.Milliseconds(),
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}

		enrichment, usage, err := p.llm.Enrich(ctx, a, settings, p.cfg.EnrichmentVersion)
		if err != nil {
			lastErr = err
			if isRetryable(err) {
				log.Warn("enrich: transient error, will retry",
					"attempt", attempt,
					"max_retries", maxRetries,
					"err", err,
				)
				continue
			}
			// Non-retryable error — mark failed immediately.
			log.Error("enrich: permanent error", "err", err)
			p.markFailed(ctx, a.ID, err)
			return
		}

		// Validate the enrichment before persisting.
		if valErr := validateEnrichment(enrichment, len(a.Tokens)); valErr != nil {
			lastErr = valErr
			log.Warn("enrich: validation failed, will retry",
				"attempt", attempt,
				"max_retries", maxRetries,
				"err", valErr,
			)
			continue
		}

		// Persist.
		enrichedAt := time.Now().UTC()
		if err := p.store.SaveEnrichment(ctx, a.ID, *enrichment, enrichedAt); err != nil {
			// The article was deleted between ListPending and this save (e.g. the
			// user removed it while enrichment was in flight). Nothing to persist
			// and nothing to fail — drop it silently.
			if errors.Is(err, ports.ErrNotFound) {
				log.Info("enrich: article deleted during enrichment, skipping")
				return
			}
			lastErr = err
			log.Error("enrich: save enrichment failed", "err", err)
			// Save failure is treated as retryable (transient DB issue).
			if isRetryable(err) {
				continue
			}
			p.markFailed(ctx, a.ID, err)
			return
		}

		log.Info("enrich: article enriched",
			"prompt_tokens", usage.PromptTokens,
			"completion_tokens", usage.CompletionTokens,
			"total_tokens", usage.TotalTokens,
		)
		return
	}

	// Exhausted retries.
	log.Error("enrich: retries exhausted", "attempts", maxRetries+1, "last_err", lastErr)
	if lastErr != nil {
		p.markFailed(ctx, a.ID, lastErr)
	}
}

// markFailed sets the article status to failed with the given error message.
func (p *Pool) markFailed(ctx context.Context, id string, err error) {
	if setErr := p.store.SetStatus(ctx, id, model.StatusFailed, err.Error()); setErr != nil {
		slog.Error("enrich: failed to set status=failed",
			"article_id", id,
			"set_err", setErr,
			"original_err", err,
		)
	}
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

// ValidationError is returned when the LLM-produced enrichment contains token
// indices that are out of range for the article.
type ValidationError struct {
	Field   string
	Index   int
	TokenN  int
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("enrich: validation: %s[%d]: %s (token count=%d)", e.Field, e.Index, e.Message, e.TokenN)
}

// validateEnrichment checks that all token-index references in e are within
// [0, tokenCount). It also verifies Phrase.StartIndex <= Phrase.EndIndex and
// Sentence.StartIndex <= Sentence.EndIndex.
func validateEnrichment(e *model.Enrichment, tokenCount int) error {
	if e == nil {
		return errors.New("enrich: validation: nil enrichment")
	}

	for i, dw := range e.DifficultWords {
		if dw.TokenIndex < 0 || dw.TokenIndex >= tokenCount {
			return &ValidationError{
				Field:   "difficult_words",
				Index:   i,
				TokenN:  tokenCount,
				Message: fmt.Sprintf("token_index %d out of range", dw.TokenIndex),
			}
		}
	}

	for i, ph := range e.Phrases {
		if ph.StartIndex < 0 || ph.StartIndex >= tokenCount {
			return &ValidationError{
				Field:   "phrases",
				Index:   i,
				TokenN:  tokenCount,
				Message: fmt.Sprintf("start_index %d out of range", ph.StartIndex),
			}
		}
		if ph.EndIndex < 0 || ph.EndIndex >= tokenCount {
			return &ValidationError{
				Field:   "phrases",
				Index:   i,
				TokenN:  tokenCount,
				Message: fmt.Sprintf("end_index %d out of range", ph.EndIndex),
			}
		}
		if ph.StartIndex > ph.EndIndex {
			return &ValidationError{
				Field:   "phrases",
				Index:   i,
				TokenN:  tokenCount,
				Message: fmt.Sprintf("start_index %d > end_index %d", ph.StartIndex, ph.EndIndex),
			}
		}
	}

	for i, s := range e.Sentences {
		if s.StartIndex < 0 || s.StartIndex >= tokenCount {
			return &ValidationError{
				Field:   "sentences",
				Index:   i,
				TokenN:  tokenCount,
				Message: fmt.Sprintf("start_index %d out of range", s.StartIndex),
			}
		}
		if s.EndIndex < 0 || s.EndIndex >= tokenCount {
			return &ValidationError{
				Field:   "sentences",
				Index:   i,
				TokenN:  tokenCount,
				Message: fmt.Sprintf("end_index %d out of range", s.EndIndex),
			}
		}
		if s.StartIndex > s.EndIndex {
			return &ValidationError{
				Field:   "sentences",
				Index:   i,
				TokenN:  tokenCount,
				Message: fmt.Sprintf("start_index %d > end_index %d", s.StartIndex, s.EndIndex),
			}
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// backoffDuration returns the capped exponential backoff for the given attempt
// (1-based: attempt=1 is the first retry).
func backoffDuration(attempt int, base, cap time.Duration) time.Duration {
	exp := math.Pow(2, float64(attempt-1))
	d := time.Duration(float64(base) * exp)
	if d > cap {
		return cap
	}
	return d
}

// isRetryable reports whether err is a transient condition worth retrying.
// It recognises the llm.APIError.Retryable() method if available.
type retryableErr interface {
	Retryable() bool
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	var re retryableErr
	if errors.As(err, &re) {
		return re.Retryable()
	}
	return false
}
