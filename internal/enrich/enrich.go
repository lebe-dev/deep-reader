// Package enrich implements the worker pool that drives the two-stage article
// pipeline: it drains articles awaiting work from the store, runs the fetch
// stage (extract + tokenize → SaveContent) and the enrich stage (LLM call →
// validate → SaveEnrichment), and records per-stage failures so the UI can show
// which stage failed and offer a stage-aware retry.
//
// Constructor: NewPool(cfg, store, ex, llm) *Pool — satisfies
// ports.EnrichmentWorker.
package enrich

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/getsentry/sentry-go"

	"deep-reader/internal/config"
	"deep-reader/internal/model"
	"deep-reader/internal/ports"
	"deep-reader/internal/tokenize"
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

// Pool is a fixed-size worker pool that drives the fetch→enrich pipeline for
// articles awaiting work. Construct it with NewPool; run Start in its own
// goroutine.
type Pool struct {
	cfg    *config.Config
	store  ports.Store
	ex     ports.Extractor
	llm    ports.LLMClient
	notify chan struct{}
}

// NewPool creates a new Pool. It satisfies ports.EnrichmentWorker via *Pool.
// Start(ctx) must be called to launch the workers.
func NewPool(cfg *config.Config, st ports.Store, ex ports.Extractor, client ports.LLMClient) *Pool {
	return &Pool{
		cfg:    cfg,
		store:  st,
		ex:     ex,
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
			// Report a worker panic to Sentry before letting it propagate, so the
			// crash is still loud (process dies) but observable. No-op when Sentry
			// is not configured.
			defer func() {
				if r := recover(); r != nil {
					sentry.CurrentHub().Recover(r)
					sentry.Flush(2 * time.Second)
					panic(r)
				}
			}()
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

		articles, err := p.store.ListWork(ctx, batchSize)
		if err != nil {
			slog.Error("enrich: list work failed", "worker", workerID, "err", err)
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

// processArticle drives one article through whichever pipeline stages it still
// needs. Articles in queued/fetching run the fetch stage first; once fetched
// (either just now or on a prior run) they run the enrich stage. Each stage
// retries with exponential back-off and, on terminal failure, records the
// stage-specific failed status so the UI can show where it broke.
func (p *Pool) processArticle(ctx context.Context, workerID int, a *model.Article) {
	log := slog.With("worker", workerID, "article_id", a.ID)

	if a.Status == model.StatusQueued || a.Status == model.StatusFetching {
		if !p.runFetch(ctx, log, a) {
			return // fetch failed, article deleted, or ctx cancelled
		}
	}

	if a.Status == model.StatusTopupQueued {
		p.runTopUp(ctx, log, a)
		return
	}

	p.runEnrich(ctx, log, a)
}

// runTopUp performs the incremental ("top up") enrichment stage: it loads the
// existing enrichment, computes the token spans no sentence covers yet, asks the
// LLM to annotate only those gaps, merges the result into the existing
// enrichment, and persists it (status=enriched). When there are no gaps it just
// restores the enriched state. On terminal failure it records
// status=enrich_failed. Retries with back-off mirror runEnrich.
func (p *Pool) runTopUp(ctx context.Context, log *slog.Logger, a *model.Article) {
	settings, err := p.store.GetSettings(ctx)
	if err != nil {
		log.Error("enrich: topup get settings failed", "err", err)
		return
	}

	payload, err := p.store.GetArticlePayload(ctx, a.ID)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			log.Info("enrich: article deleted before topup, skipping")
			return
		}
		log.Error("enrich: topup get payload failed", "err", err)
		return
	}
	var existing model.Enrichment
	if payload.Enrichment != nil {
		existing = *payload.Enrichment
	}

	tokens := a.Tokens
	if len(tokens) == 0 {
		tokens = payload.Tokens
	}
	text := a.OriginalText
	if text == "" {
		text = payload.OriginalText
	}

	spans := uncoveredSpans(existing, len(tokens))
	if len(spans) == 0 {
		// Already fully covered — nothing to add. Restore the enriched state so
		// the article leaves the work queue (SaveEnrichment recomputes coverage).
		if err := p.store.SaveEnrichment(ctx, a.ID, existing, time.Now().UTC()); err != nil && !errors.Is(err, ports.ErrNotFound) {
			log.Error("enrich: topup save (no gaps) failed", "err", err)
		}
		log.Info("enrich: topup found no uncovered spans, nothing to do")
		return
	}
	// Widen the uncovered gaps to whole sentences so each span the LLM receives
	// contains complete sentences (it now sees only the spanned tokens, not the
	// whole article — see llm.buildSpanPrompt).
	spans = expandSpansToSentences(text, tokens, spans)

	p.setStatus(ctx, log, a.ID, model.StatusEnriching, "")

	maxRetries := p.cfg.LLMMaxRetries
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if ctx.Err() != nil {
			return
		}
		if attempt > 0 && !p.backoff(ctx, log, attempt) {
			return
		}

		addition, usage, err := p.llm.EnrichSpans(ctx, a, settings, p.cfg.EnrichmentVersion, spans)
		if err != nil {
			lastErr = err
			if isRetryable(err) {
				log.Warn("enrich: topup transient error, will retry", "attempt", attempt, "max_retries", maxRetries, "err", err)
				continue
			}
			log.Error("enrich: topup permanent error", "err", err)
			p.setFailed(ctx, a.ID, model.StatusEnrichFailed, err)
			return
		}
		if addition == nil {
			addition = &model.Enrichment{}
		}

		// Drop individually-invalid additions instead of failing the whole pass
		// (mirrors the per-chunk path): a malformed entry costs only itself, and
		// any still-uncovered span surfaces again on the next top-up.
		clean := sanitizeEnrichment(*addition, tokens)
		if dropped := droppedCount(*addition, clean); dropped > 0 {
			log.Warn("enrich: topup dropped invalid annotations", "dropped", dropped)
		}
		merged := mergeEnrichment(existing, clean)

		if err := p.store.SaveEnrichment(ctx, a.ID, merged, time.Now().UTC()); err != nil {
			if errors.Is(err, ports.ErrNotFound) {
				log.Info("enrich: article deleted during topup, skipping")
				return
			}
			lastErr = err
			log.Error("enrich: topup save enrichment failed", "err", err)
			if isRetryable(err) {
				continue
			}
			p.setFailed(ctx, a.ID, model.StatusEnrichFailed, err)
			return
		}

		log.Info("enrich: article topped up (incremental)",
			"spans", len(spans),
			"prompt_tokens", usage.PromptTokens,
			"completion_tokens", usage.CompletionTokens,
			"total_tokens", usage.TotalTokens,
		)
		return
	}

	log.Error("enrich: topup retries exhausted", "attempts", maxRetries+1, "last_err", lastErr)
	if lastErr != nil {
		p.setFailed(ctx, a.ID, model.StatusEnrichFailed, lastErr)
	}
}

// ErrBotWall is returned (terminal, non-retryable) when the fetched content is
// recognised as a bot-verification / captcha interstitial (e.g. Cloudflare's
// "Just a moment", a Vercel Security Checkpoint) rather than the real article.
// It is detected in the fetch stage before any LLM call, so no tokens are spent
// annotating a challenge page. The matched signature is included for diagnostics.
var ErrBotWall = errors.New("content blocked by bot-verification / captcha")

// botWallMaxWords is the upper bound on the body word count for content to be
// treated as a bot-wall on a signature match. Challenge pages are a handful of
// words; real articles are far longer, so a long body with a matching phrase is
// almost certainly an article discussing the topic, not the wall itself.
const botWallMaxWords = 120

// botWallSignaturesFor returns the signature list to match against: the user's
// custom override (Settings.BotWallSignatures, one per line) when set, otherwise
// the built-in model.DefaultBotWallSignatures.
func botWallSignaturesFor(settings model.Settings) []string {
	if sigs := model.ParseBotWallSignatures(settings.BotWallSignatures); len(sigs) > 0 {
		return sigs
	}
	return model.DefaultBotWallSignatures
}

// detectBotWall reports a non-empty reason (the matched signature) when result
// looks like a bot-verification / captcha interstitial: a known signature in the
// title or body AND a body short enough to be a challenge page. It returns "" for
// real content. signatures are expected lowercased (see model.ParseBotWallSignatures).
// Detection happens before SaveContent and any LLM stage.
func detectBotWall(result *ports.ExtractResult, signatures []string) string {
	if result == nil {
		return ""
	}
	if len(strings.Fields(result.Text)) > botWallMaxWords {
		return ""
	}
	hay := strings.ToLower(result.Title + "\n" + result.Text)
	for _, sig := range signatures {
		if strings.Contains(hay, sig) {
			return sig
		}
	}
	return ""
}

// runFetch performs the fetch/extract stage: it flips the article to
// status=fetching, extracts and tokenizes the content with retries, and on
// success persists it (status=fetched), updating a in place so the enrich stage
// can proceed. It returns true when the content was saved and enrichment should
// continue, false on terminal failure, deletion, or cancellation.
func (p *Pool) runFetch(ctx context.Context, log *slog.Logger, a *model.Article) bool {
	p.setStatus(ctx, log, a.ID, model.StatusFetching, "")

	// Load the bot-wall signature list once for this fetch. A settings read error
	// must not block fetching — fall back to the built-in defaults.
	settings, err := p.store.GetSettings(ctx)
	if err != nil {
		log.Warn("enrich: get settings for bot-wall detection failed, using defaults", "err", err)
	}
	signatures := botWallSignaturesFor(settings)

	maxRetries := p.cfg.LLMMaxRetries
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if ctx.Err() != nil {
			return false
		}
		if attempt > 0 && !p.backoff(ctx, log, attempt) {
			return false
		}

		result, err := p.ex.Extract(ctx, a.SourceURL)
		if err != nil {
			lastErr = err
			if isRetryable(err) {
				log.Warn("enrich: transient fetch error, will retry", "attempt", attempt, "max_retries", maxRetries, "err", err)
				continue
			}
			log.Error("enrich: permanent fetch error", "err", err)
			p.setFailed(ctx, a.ID, model.StatusFetchFailed, err)
			return false
		}

		// Guard against bot-verification / captcha interstitials: markdown.new (and
		// the readability fallback) happily convert a challenge page into "content",
		// so catch it here — before SaveContent and any LLM call — and mark the
		// article blocked rather than spending tokens annotating a captcha. Not
		// retried automatically (the wall won't clear on its own); a manual retry
		// re-runs the fetch.
		if reason := detectBotWall(result, signatures); reason != "" {
			log.Warn("enrich: bot-wall/captcha detected, aborting before LLM", "reason", reason)
			p.setFailed(ctx, a.ID, model.StatusBlocked, fmt.Errorf("%w: %s", ErrBotWall, reason))
			return false
		}

		// Decode HTML entities before tokenizing so token byte offsets stay
		// consistent with the stored OriginalText.
		text := html.UnescapeString(result.Text)
		tokens := tokenize.Tokenize(text)

		update := ports.ContentUpdate{
			SourceURL:    result.CanonicalURL,
			Title:        html.UnescapeString(result.Title),
			Author:       html.UnescapeString(result.Author),
			SourceDomain: result.Domain,
			Lang:         result.Lang,
			Text:         text,
			Tokens:       tokens,
		}
		if err := p.store.SaveContent(ctx, a.ID, update); err != nil {
			if errors.Is(err, ports.ErrNotFound) {
				log.Info("enrich: article deleted during fetch, skipping")
				return false
			}
			lastErr = err
			log.Error("enrich: save content failed", "err", err)
			if isRetryable(err) {
				continue
			}
			p.setFailed(ctx, a.ID, model.StatusFetchFailed, err)
			return false
		}

		// Reflect the saved content into the in-memory article for the enrich
		// stage (token count drives enrichment validation).
		a.SourceURL = update.SourceURL
		a.Title = update.Title
		a.Author = update.Author
		a.SourceDomain = update.SourceDomain
		a.Lang = update.Lang
		a.OriginalText = update.Text
		a.Tokens = update.Tokens
		a.Status = model.StatusFetched

		log.Info("enrich: content fetched", "domain", update.SourceDomain, "token_count", len(tokens))
		return true
	}

	log.Error("enrich: fetch retries exhausted", "attempts", maxRetries+1, "last_err", lastErr)
	if lastErr != nil {
		p.setFailed(ctx, a.ID, model.StatusFetchFailed, lastErr)
	}
	return false
}

// runEnrich performs the step-wise enrichment stage. Rather than asking the LLM
// to annotate the whole article in one completion (which truncates the JSON on
// long articles), it: (1) produces a short summary (best-effort, used as
// cross-chunk context), (2) splits the token stream into sentence-aligned
// chunks, and (3) annotates each chunk with its own bounded LLM call, merging
// and persisting the result after every chunk so progress survives a crash and
// the coverage signal grows incrementally. Only the final chunk flips the
// article to status=enriched; intermediate saves keep it in status=enriching so
// an interrupted article is re-selected and resumed. On terminal failure it
// records status=enrich_failed, preserving whatever chunks already succeeded.
func (p *Pool) runEnrich(ctx context.Context, log *slog.Logger, a *model.Article) {
	settings, err := p.store.GetSettings(ctx)
	if err != nil {
		log.Error("enrich: get settings failed", "err", err)
		return
	}

	p.setStatus(ctx, log, a.ID, model.StatusEnriching, "")

	// Step 1: summary (best-effort — a summary failure must not block translation).
	if strings.TrimSpace(a.Summary) == "" {
		if summary, ok := p.runSummarize(ctx, log, a, settings); ok {
			a.Summary = summary
		}
	}

	// Resume support: load any enrichment a prior (interrupted or partially
	// failed) run already persisted so covered chunks are skipped.
	running := p.loadExistingEnrichment(ctx, log, a.ID)

	chunks := chunkSpans(a.OriginalText, a.Tokens, p.chunkTokens(settings))
	if len(chunks) == 0 {
		// No tokens to annotate — persist (possibly empty) enrichment as enriched.
		if err := p.store.SaveEnrichment(ctx, a.ID, running, time.Now().UTC()); err != nil && !errors.Is(err, ports.ErrNotFound) {
			log.Error("enrich: save (no chunks) failed", "err", err)
		}
		return
	}

	var lastErr error
	for ci, span := range chunks {
		if ctx.Err() != nil {
			return
		}
		if spanCovered(running, span) {
			continue // already annotated by a prior run
		}

		merged, err := p.enrichChunk(ctx, log, a, settings, running, span, ci, len(chunks))
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			lastErr = err
			continue // keep going; preserve the chunks that did succeed
		}
		running = merged

		if err := p.store.SaveEnrichmentProgress(ctx, a.ID, running); err != nil {
			if errors.Is(err, ports.ErrNotFound) {
				log.Info("enrich: article deleted during enrichment, skipping")
				return
			}
			lastErr = err
			log.Error("enrich: save progress failed", "chunk", ci, "err", err)
		}
	}

	if lastErr != nil {
		log.Error("enrich: one or more chunks failed", "chunks", len(chunks), "last_err", lastErr)
		p.setFailed(ctx, a.ID, model.StatusEnrichFailed, lastErr)
		return
	}

	// All chunks done — flip to enriched.
	if err := p.store.SaveEnrichment(ctx, a.ID, running, time.Now().UTC()); err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			log.Info("enrich: article deleted during enrichment, skipping")
			return
		}
		log.Error("enrich: final save enrichment failed", "err", err)
		p.setFailed(ctx, a.ID, model.StatusEnrichFailed, err)
		return
	}
	log.Info("enrich: article enriched (step-wise)", "chunks", len(chunks))
}

// runSummarize runs the summary step with retry/backoff and persists the result.
// It returns the summary and ok=true on success. A summary failure is
// non-terminal (the article can still be translated without it), so ok=false
// just means "continue without a summary".
func (p *Pool) runSummarize(ctx context.Context, log *slog.Logger, a *model.Article, settings model.Settings) (string, bool) {
	maxRetries := p.cfg.LLMMaxRetries
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if ctx.Err() != nil {
			return "", false
		}
		if attempt > 0 && !p.backoff(ctx, log, attempt) {
			return "", false
		}

		summary, usage, err := p.llm.Summarize(ctx, a, settings)
		if err != nil {
			if isRetryable(err) {
				log.Warn("enrich: summary transient error, will retry", "attempt", attempt, "max_retries", maxRetries, "err", err)
				continue
			}
			log.Warn("enrich: summary failed, continuing without it", "err", err)
			return "", false
		}
		if strings.TrimSpace(summary) == "" {
			log.Warn("enrich: summary empty, continuing without it")
			return "", false
		}

		if err := p.store.SaveSummary(ctx, a.ID, summary); err != nil {
			if errors.Is(err, ports.ErrNotFound) {
				return "", false
			}
			log.Warn("enrich: save summary failed, continuing", "err", err)
			return summary, true // we still have it in memory for chunk context
		}
		log.Info("enrich: summary generated",
			"summary_bytes", len(summary),
			"prompt_tokens", usage.PromptTokens,
			"completion_tokens", usage.CompletionTokens,
		)
		return summary, true
	}
	log.Warn("enrich: summary retries exhausted, continuing without it")
	return "", false
}

// enrichChunk annotates a single token span with retry/backoff, merges the
// addition into the running enrichment, validates the merged result, and returns
// it. It returns errArticleDeleted if the article vanished, ctx.Err() on
// cancellation, or the last error after exhausting retries.
func (p *Pool) enrichChunk(ctx context.Context, log *slog.Logger, a *model.Article, settings model.Settings, existing model.Enrichment, span model.Span, idx, total int) (model.Enrichment, error) {
	maxRetries := p.cfg.LLMMaxRetries
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if ctx.Err() != nil {
			return model.Enrichment{}, ctx.Err()
		}
		if attempt > 0 && !p.backoff(ctx, log, attempt) {
			return model.Enrichment{}, ctx.Err()
		}

		addition, usage, err := p.llm.EnrichSpans(ctx, a, settings, p.cfg.EnrichmentVersion, []model.Span{span})
		if err != nil {
			lastErr = err
			if isRetryable(err) {
				log.Warn("enrich: chunk transient error, will retry", "chunk", idx+1, "of", total, "attempt", attempt, "err", err)
				continue
			}
			log.Error("enrich: chunk permanent error", "chunk", idx+1, "of", total, "err", err)
			return model.Enrichment{}, err
		}
		if addition == nil {
			addition = &model.Enrichment{}
		}

		// Drop any individually-invalid annotation instead of failing the whole
		// chunk: a single empty translation or drifted phrase costs only that one
		// entry, and whatever stays uncovered is picked up by a later top-up pass.
		clean := sanitizeEnrichment(*addition, a.Tokens)
		if dropped := droppedCount(*addition, clean); dropped > 0 {
			log.Warn("enrich: chunk dropped invalid annotations", "chunk", idx+1, "of", total, "dropped", dropped)
		}
		merged := mergeEnrichment(existing, clean)

		log.Info("enrich: chunk done",
			"chunk", idx+1, "of", total,
			"span_start", span.Start, "span_end", span.End,
			"prompt_tokens", usage.PromptTokens,
			"completion_tokens", usage.CompletionTokens,
		)
		return merged, nil
	}

	log.Error("enrich: chunk retries exhausted", "chunk", idx+1, "of", total, "attempts", maxRetries+1, "last_err", lastErr)
	return model.Enrichment{}, lastErr
}

// chunkTokens resolves the effective step-wise enrichment window size: the
// user's per-settings override (Settings.ChunkTokens) when set to a positive
// value, otherwise the deployment default (config.LLMChunkTokens).
func (p *Pool) chunkTokens(settings model.Settings) int {
	if settings.ChunkTokens > 0 {
		return settings.ChunkTokens
	}
	return p.cfg.LLMChunkTokens
}

// loadExistingEnrichment returns the enrichment already persisted for the
// article (for resuming a partially-completed run), or an empty enrichment when
// none exists or the lookup fails (treated as "start fresh").
func (p *Pool) loadExistingEnrichment(ctx context.Context, log *slog.Logger, id string) model.Enrichment {
	payload, err := p.store.GetArticlePayload(ctx, id)
	if err != nil {
		if !errors.Is(err, ports.ErrNotFound) {
			log.Warn("enrich: load existing enrichment failed, starting fresh", "err", err)
		}
		return model.Enrichment{}
	}
	if payload.Enrichment != nil {
		return *payload.Enrichment
	}
	return model.Enrichment{}
}

// spanCovered reports whether every token in span is already covered by a
// sentence in e — i.e. the chunk was annotated by a prior run and can be skipped.
func spanCovered(e model.Enrichment, span model.Span) bool {
	for i := span.Start; i <= span.End; i++ {
		if !tokenCovered(e, i) {
			return false
		}
	}
	return true
}

// tokenCovered reports whether token index i falls within at least one sentence
// range in e.
func tokenCovered(e model.Enrichment, i int) bool {
	for _, s := range e.Sentences {
		if i >= s.StartIndex && i <= s.EndIndex {
			return true
		}
	}
	return false
}

// backoff sleeps for the attempt's exponential back-off, returning false if ctx
// is cancelled during the wait.
func (p *Pool) backoff(ctx context.Context, log *slog.Logger, attempt int) bool {
	wait := backoffDuration(attempt, baseBackoff, maxBackoff)
	log.Info("enrich: backing off before retry", "attempt", attempt, "wait_ms", wait.Milliseconds())
	select {
	case <-ctx.Done():
		return false
	case <-time.After(wait):
		return true
	}
}

// setStatus updates the article's status (best-effort; logs on failure). Used
// for the in-flight states (fetching/enriching) where a failure to flip the UI
// state must not abort the pipeline.
func (p *Pool) setStatus(ctx context.Context, log *slog.Logger, id, status, errMsg string) {
	if err := p.store.SetStatus(ctx, id, status, errMsg); err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return // article deleted; the next store call handles it
		}
		log.Warn("enrich: set status failed", "status", status, "err", err)
	}
}

// setFailed records a terminal stage failure with its error message. When the
// error carries a raw LLM response that failed to decode (rawResponseOf), that
// response is persisted alongside the error so the UI can show it verbatim.
func (p *Pool) setFailed(ctx context.Context, id, status string, err error) {
	if setErr := p.store.SetFailed(ctx, id, status, err.Error(), rawResponseOf(err)); setErr != nil {
		slog.Error("enrich: failed to set failed status",
			"article_id", id,
			"status", status,
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
// Sentence.StartIndex <= Sentence.EndIndex, and that each Phrase.Text matches
// the words actually spanned by its [StartIndex, EndIndex] range — the guard
// against over-wide / drifted phrase ranges (see model.Phrase.Text).
func validateEnrichment(e *model.Enrichment, tokens []model.Token) error {
	if e == nil {
		return errors.New("enrich: validation: nil enrichment")
	}
	tokenCount := len(tokens)

	for i, dw := range e.DifficultWords {
		if dw.TokenIndex < 0 || dw.TokenIndex >= tokenCount {
			return &ValidationError{
				Field:   "difficult_words",
				Index:   i,
				TokenN:  tokenCount,
				Message: fmt.Sprintf("token_index %d out of range", dw.TokenIndex),
			}
		}
		if strings.TrimSpace(dw.Translation) == "" {
			return &ValidationError{
				Field:   "difficult_words",
				Index:   i,
				TokenN:  tokenCount,
				Message: "empty translation",
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
		if strings.TrimSpace(ph.Translation) == "" {
			return &ValidationError{
				Field:   "phrases",
				Index:   i,
				TokenN:  tokenCount,
				Message: "empty translation",
			}
		}
		if strings.TrimSpace(ph.Text) == "" {
			return &ValidationError{
				Field:   "phrases",
				Index:   i,
				TokenN:  tokenCount,
				Message: "empty text",
			}
		}
		// Indices are already known valid here. The echoed text must spell the
		// same word sequence as the claimed range; a mismatch means the model
		// drifted the range (e.g. a one-word term tagged onto a whole clause).
		want := normalizePhraseText(joinTokenText(tokens, ph.StartIndex, ph.EndIndex))
		if got := normalizePhraseText(ph.Text); got != want {
			return &ValidationError{
				Field:   "phrases",
				Index:   i,
				TokenN:  tokenCount,
				Message: fmt.Sprintf("text %q does not match tokens [%d,%d] (%q)", ph.Text, ph.StartIndex, ph.EndIndex, want),
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
		if strings.TrimSpace(s.Translation) == "" {
			return &ValidationError{
				Field:   "sentences",
				Index:   i,
				TokenN:  tokenCount,
				Message: "empty translation",
			}
		}
	}

	return nil
}

// sanitizeEnrichment returns a copy of e with every individually-invalid
// annotation dropped, rather than rejecting the whole batch. The step-wise and
// incremental enrich paths feed each LLM addition through this before merging,
// so one bad entry — an empty translation, an out-of-range index, a phrase
// whose echoed text drifted from its range — costs only that annotation instead
// of failing the chunk and, after exhausted retries, the whole article (the bug
// behind "chunk validation failed … empty translation"). Whatever is dropped
// stays uncovered and is picked up by a later top-up pass.
//
// The accept/reject predicates mirror validateEnrichment exactly; only the
// action differs (skip the entry vs. return an error). Glossary items are not
// validated, so they pass through unchanged. The input is never mutated.
func sanitizeEnrichment(e model.Enrichment, tokens []model.Token) model.Enrichment {
	tokenCount := len(tokens)

	var words []model.DifficultWord
	for _, dw := range e.DifficultWords {
		if dw.TokenIndex < 0 || dw.TokenIndex >= tokenCount {
			continue
		}
		if strings.TrimSpace(dw.Translation) == "" {
			continue
		}
		words = append(words, dw)
	}

	var phrases []model.Phrase
	for _, ph := range e.Phrases {
		if ph.StartIndex < 0 || ph.StartIndex >= tokenCount {
			continue
		}
		if ph.EndIndex < 0 || ph.EndIndex >= tokenCount {
			continue
		}
		if ph.StartIndex > ph.EndIndex {
			continue
		}
		if strings.TrimSpace(ph.Translation) == "" || strings.TrimSpace(ph.Text) == "" {
			continue
		}
		want := normalizePhraseText(joinTokenText(tokens, ph.StartIndex, ph.EndIndex))
		if normalizePhraseText(ph.Text) != want {
			continue
		}
		phrases = append(phrases, ph)
	}

	var sentences []model.Sentence
	for _, s := range e.Sentences {
		if s.StartIndex < 0 || s.StartIndex >= tokenCount {
			continue
		}
		if s.EndIndex < 0 || s.EndIndex >= tokenCount {
			continue
		}
		if s.StartIndex > s.EndIndex {
			continue
		}
		if strings.TrimSpace(s.Translation) == "" {
			continue
		}
		sentences = append(sentences, s)
	}

	return model.Enrichment{
		DifficultWords: words,
		Phrases:        phrases,
		Sentences:      sentences,
		Glossary:       e.Glossary,
	}
}

// droppedCount reports how many word/phrase/sentence annotations sanitizeEnrichment
// removed (clean must be the sanitized form of original). Glossary is excluded
// because it is never sanitized.
func droppedCount(original, clean model.Enrichment) int {
	return (len(original.DifficultWords) - len(clean.DifficultWords)) +
		(len(original.Phrases) - len(clean.Phrases)) +
		(len(original.Sentences) - len(clean.Sentences))
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// uncoveredSpans returns the contiguous, inclusive token-index ranges in
// [0, tokenCount) that no sentence in e covers. It mirrors
// store.sentenceCoverage's covered-set computation (a token is covered when it
// falls within at least one sentence range) and returns the complementary gaps —
// the only ranges the incremental ("top up") enrich re-sends to the LLM. Returns
// nil when tokenCount <= 0 or every token is already covered.
func uncoveredSpans(e model.Enrichment, tokenCount int) []model.Span {
	if tokenCount <= 0 {
		return nil
	}
	covered := make([]bool, tokenCount)
	for _, s := range e.Sentences {
		start := max(s.StartIndex, 0)
		end := min(s.EndIndex, tokenCount-1)
		for i := start; i <= end; i++ {
			covered[i] = true
		}
	}

	var spans []model.Span
	for i := 0; i < tokenCount; {
		if covered[i] {
			i++
			continue
		}
		start := i
		for i < tokenCount && !covered[i] {
			i++
		}
		spans = append(spans, model.Span{Start: start, End: i - 1})
	}
	return spans
}

// mergeEnrichment combines an existing enrichment with the additions from an
// incremental ("top up") enrich pass. It is additive and the existing data
// always wins on conflict, so re-running a top up is idempotent:
//   - difficult_words: append additions for token indices not already present.
//   - phrases: append additions whose [start,end] range is not already present.
//   - glossary: append additions for terms not already present.
//   - sentences: append additions that do not overlap any token already covered
//     by an existing sentence, keeping the sentence set non-overlapping (the
//     reader maps each token to a single sentence).
//
// The returned enrichment uses fresh slices so the inputs are never mutated.
func mergeEnrichment(existing, addition model.Enrichment) model.Enrichment {
	merged := model.Enrichment{
		DifficultWords: append([]model.DifficultWord(nil), existing.DifficultWords...),
		Phrases:        append([]model.Phrase(nil), existing.Phrases...),
		Sentences:      append([]model.Sentence(nil), existing.Sentences...),
		Glossary:       append([]model.GlossaryItem(nil), existing.Glossary...),
	}

	seenWord := make(map[int]bool, len(existing.DifficultWords))
	for _, w := range existing.DifficultWords {
		seenWord[w.TokenIndex] = true
	}
	for _, w := range addition.DifficultWords {
		if seenWord[w.TokenIndex] {
			continue
		}
		seenWord[w.TokenIndex] = true
		merged.DifficultWords = append(merged.DifficultWords, w)
	}

	type phraseKey struct{ start, end int }
	seenPhrase := make(map[phraseKey]bool, len(existing.Phrases))
	for _, ph := range existing.Phrases {
		seenPhrase[phraseKey{ph.StartIndex, ph.EndIndex}] = true
	}
	for _, ph := range addition.Phrases {
		key := phraseKey{ph.StartIndex, ph.EndIndex}
		if seenPhrase[key] {
			continue
		}
		seenPhrase[key] = true
		merged.Phrases = append(merged.Phrases, ph)
	}

	seenTerm := make(map[string]bool, len(existing.Glossary))
	for _, g := range existing.Glossary {
		seenTerm[g.Term] = true
	}
	for _, g := range addition.Glossary {
		if seenTerm[g.Term] {
			continue
		}
		seenTerm[g.Term] = true
		merged.Glossary = append(merged.Glossary, g)
	}

	covered := make(map[int]bool)
	for _, s := range existing.Sentences {
		for i := s.StartIndex; i <= s.EndIndex; i++ {
			covered[i] = true
		}
	}
	for _, s := range addition.Sentences {
		if sentenceOverlaps(covered, s) {
			continue
		}
		merged.Sentences = append(merged.Sentences, s)
		for i := s.StartIndex; i <= s.EndIndex; i++ {
			covered[i] = true
		}
	}

	return merged
}

// sentenceOverlaps reports whether any token in s's inclusive range is already
// marked covered.
func sentenceOverlaps(covered map[int]bool, s model.Sentence) bool {
	for i := s.StartIndex; i <= s.EndIndex; i++ {
		if covered[i] {
			return true
		}
	}
	return false
}

// joinTokenText concatenates the text of tokens [start, end] (inclusive) with a
// single space between each. The range is assumed valid (callers validate
// indices first).
func joinTokenText(tokens []model.Token, start, end int) string {
	parts := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		parts = append(parts, tokens[i].Text)
	}
	return strings.Join(parts, " ")
}

// normalizePhraseText reduces a string to its lowercased word sequence: each run
// of non-alphanumeric runes (whitespace, punctuation) collapses to a single
// separating space, and leading/trailing separators are dropped. This makes the
// phrase-text comparison robust to punctuation and spacing differences between
// the model's echoed text and the tokenized source, so only the actual word
// sequence has to agree.
func normalizePhraseText(s string) string {
	var b strings.Builder
	pendingSpace := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if pendingSpace && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			pendingSpace = false
			continue
		}
		pendingSpace = true
	}
	return b.String()
}

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

// rawResponder is implemented by errors that carry the raw LLM output that
// failed to decode (llm.DecodeError). The accessor is duck-typed so this
// package need not import llm.
type rawResponder interface {
	RawResponse() string
}

// rawResponseOf returns the raw LLM response carried by err, or "" when err does
// not carry one (e.g. a fetch failure, an HTTP/network error, or a validation
// error).
func rawResponseOf(err error) string {
	if err == nil {
		return ""
	}
	var rr rawResponder
	if errors.As(err, &rr) {
		return rr.RawResponse()
	}
	return ""
}
