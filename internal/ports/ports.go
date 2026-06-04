// Package ports defines the integration contract for Deep Reader: the Go
// interfaces that decouple the HTTP/api layer from its dependencies (storage,
// extraction, LLM, ingestion, enrichment), plus the exact concrete constructor
// signatures every implementer must provide.
//
// Conventions enforced across this package:
//   - context.Context is the first argument on every method that performs I/O.
//   - Domain types come from package model; this package adds only the small
//     value types (Usage, ExtractResult) that are not part of the persisted
//     domain model.
//   - Implementations live in sibling internal packages (store, extract, llm,
//     ingest, enrich) and the pure tokenizer in internal/tokenize. The api
//     package depends only on these interfaces, never on concrete types.
//
// # INTEGRATION CONTRACT
//
// Downstream agents MUST provide exactly these constructors and method sets so
// that cmd/server can wire the graph without adaptation. Signatures are binding.
//
// Storage (package internal/store):
//
//	func NewSQLite(ctx context.Context, cfg *config.Config) (*store.SQLite, error)
//	    // *store.SQLite implements ports.Store.
//	    // Opens the SQLite DB at cfg.DatabasePath, applies goose migrations
//	    // (embedded migrations run on open), seeds the singleton settings row if
//	    // absent, and configures the single-writer / read-pool connection model.
//	    // Also provide: func (s *store.SQLite) Close() error
//
// Extraction (package internal/extract):
//
//	func New(cfg *config.Config) *extract.Extractor
//	    // *extract.Extractor implements ports.Extractor.
//	    // Fetches the URL (scheme whitelist http/https, private-range/SSRF
//	    // guard, max body size, READABILITY_TIMEOUT) and runs go-readability.
//
// LLM (package internal/llm):
//
//	func New(cfg *config.Config) *llm.Client
//	    // *llm.Client implements ports.LLMClient.
//	    // Thin OpenAI-compatible wrapper using cfg.LLMAPIBaseURL/Key/Model and
//	    // cfg.LLMRequestTimeout. Retries/backoff are owned by the enrich Pool,
//	    // not the client (the client performs one attempt per Enrich call).
//
// Ingestion (package internal/ingest):
//
//	func New(cfg *config.Config, st ports.Store, worker ports.EnrichmentWorker) *ingest.Ingestor
//	    // *ingest.Ingestor implements ports.Ingestor.
//	    // Add: normalize + dedup by URL hash, persist as queued, then
//	    // worker.Notify(). Retry: store.RetryArticle(id) then worker.Notify().
//
// Enrichment worker (package internal/enrich):
//
//	func NewPool(cfg *config.Config, st ports.Store, ex ports.Extractor, client ports.LLMClient) *enrich.Pool
//	    // *enrich.Pool implements ports.EnrichmentWorker.
//	    // Start(ctx) launches cfg.LLMMaxConcurrent workers that drain
//	    // store.ListWork; Notify() wakes them. Per article it runs the fetch
//	    // stage (ex.Extract → tokenize → store.SaveContent) then the enrich
//	    // stage (client.Enrich → store.SaveEnrichment). On terminal failure it
//	    // SetStatus(fetch_failed|enrich_failed, err). Retries with backoff up to
//	    // cfg.LLMMaxRetries.
//
// Tokenizer (package internal/tokenize):
//
//	func Tokenize(text string) []model.Token
//	    // Pure, deterministic. Splits on whitespace/punctuation, keeps exact
//	    // byte offsets (Token.Start/End index into text), treats contractions
//	    // (don't, it's) as a single token. No I/O, no context.
//
// HTTP / static (package internal/api):
//
//	func New(cfg *config.Config, st ports.Store, ing ports.Ingestor) *api.Server   // wiring TBD by api agent
//	    // The api package serves the embedded PWA via web.FS() (import
//	    // "deep-reader/web"); there is NO STATIC_DIR. /healthz and the public auth
//	    // endpoints (/api/config, /api/setup, /api/login) are unauthenticated; all
//	    // other /api/* routes require a valid session bearer token.
package ports

import (
	"context"
	"errors"
	"time"

	"deep-reader/internal/model"
)

// Sentinel errors that Store implementations return and callers match with
// errors.Is. They are part of the integration contract: the api layer maps
// ErrNotFound to 404 and ErrDuplicate is consumed by ingestion dedup.
var (
	// ErrNotFound is returned when a requested entity does not exist.
	ErrNotFound = errors.New("not found")
	// ErrDuplicate is returned by CreateArticle when the url_hash already
	// exists. Ingestion treats this as a dedup signal, not a failure.
	ErrDuplicate = errors.New("duplicate")
	// ErrAlreadyInitialized is returned by CreateUser when the single built-in
	// account already exists. The setup flow maps it to 409 Conflict.
	ErrAlreadyInitialized = errors.New("already initialized")
)

// Store is the persistence boundary. The source of truth is SQLite; all methods
// that touch the DB take a context for cancellation/timeouts. Concrete
// constructor: store.NewSQLite(ctx, cfg) (*store.SQLite, error).
type Store interface {
	// IsInitialized reports whether the single built-in account has been created.
	// The service is "initialized" once a user exists; until then the client is
	// directed to the setup flow.
	IsInitialized(ctx context.Context) (bool, error)

	// CreateUser creates the single built-in account with the given username and
	// bcrypt password hash. It returns ErrAlreadyInitialized if an account
	// already exists (setup is a one-time operation).
	CreateUser(ctx context.Context, username, passwordHash string) error

	// GetUser returns the built-in account, or ErrNotFound if the service is not
	// yet initialized.
	GetUser(ctx context.Context) (*model.User, error)

	// CreateSession persists a login session keyed by the SHA-256 hash of its
	// bearer token, stamped with createdAt.
	CreateSession(ctx context.Context, tokenHash string, createdAt time.Time) error

	// SessionExists reports whether a session with the given token hash exists.
	// It is the hot path for the auth middleware.
	SessionExists(ctx context.Context, tokenHash string) (bool, error)

	// DeleteSession removes the session with the given token hash (logout). It is
	// a no-op if the session does not exist.
	DeleteSession(ctx context.Context, tokenHash string) error

	// GetSettings returns the singleton settings row, seeding defaults if it
	// was never written.
	GetSettings(ctx context.Context) (model.Settings, error)

	// UpdateSettings applies a partial patch (nil fields unchanged), stamps
	// UpdatedAt, and returns the resulting settings.
	UpdateSettings(ctx context.Context, patch model.SettingsPatch) (model.Settings, error)

	// CreateArticle inserts a new article (typically status=pending). The
	// caller populates ID, URLHash, tokens, timestamps, etc. Returns
	// ErrDuplicate if URLHash already exists.
	CreateArticle(ctx context.Context, a *model.Article) error

	// GetArticleByHash returns the article with the given url_hash, or
	// ErrNotFound. Used for ingestion dedup.
	GetArticleByHash(ctx context.Context, urlHash string) (*model.Article, error)

	// ListArticleMeta returns library metadata for articles updated at or after
	// `since`. Pass the zero time to list everything (full sync).
	ListArticleMeta(ctx context.Context, since time.Time) ([]model.ArticleMeta, error)

	// GetArticle returns the full server-side article record, or ErrNotFound.
	GetArticle(ctx context.Context, id string) (*model.Article, error)

	// GetArticlePayload returns the client-facing payload (tokens + enrichment),
	// or ErrNotFound. Enrichment is nil when the article is not yet enriched.
	GetArticlePayload(ctx context.Context, id string) (*model.ArticlePayload, error)

	// DeleteArticle removes the article (and its enrichment/progress) by id.
	// Returns ErrNotFound if it did not exist.
	DeleteArticle(ctx context.Context, id string) error

	// SetStatus updates an article's status and error text, stamping UpdatedAt
	// (and EnrichedAt when status==enriched is set via SaveEnrichment, not
	// here). errMsg is stored only for the *_failed states; pass "" otherwise.
	// It always clears any captured raw LLM response (see SetFailed).
	SetStatus(ctx context.Context, id, status, errMsg string) error

	// SetFailed records a terminal stage failure: status (fetch_failed or
	// enrich_failed), the error message, and rawLLMResponse — the verbatim model
	// output captured when the enrichment response could not be decoded (empty
	// for fetch failures or non-decode errors). Stamps UpdatedAt. Returns
	// ErrNotFound if the article does not exist.
	SetFailed(ctx context.Context, id, status, errMsg, rawLLMResponse string) error

	// GetArticleRaw returns the raw LLM response (and error/status) captured for
	// an article, or ErrNotFound. Raw is empty when nothing was captured.
	GetArticleRaw(ctx context.Context, id string) (*model.ArticleRaw, error)

	// SaveContent persists fetched/extracted content for an article and advances
	// its status to fetched (clearing any prior error). Returns ErrNotFound if
	// the article was deleted while the fetch was in flight.
	SaveContent(ctx context.Context, id string, c ContentUpdate) error

	// SaveEnrichment persists the enrichment blob, sets status=enriched, sets
	// EnrichedAt=enrichedAt, and stamps UpdatedAt. Atomic with the status flip.
	SaveEnrichment(ctx context.Context, id string, e model.Enrichment, enrichedAt time.Time) error

	// SaveSummary persists the article's summary text (the first step of the
	// step-wise enrichment), stamping UpdatedAt without changing status. Returns
	// ErrNotFound if the article was deleted in the meantime.
	SaveSummary(ctx context.Context, id, summary string) error

	// SaveEnrichmentProgress persists a partial enrichment blob produced by an
	// intermediate step of the step-wise enrichment and recomputes the coverage
	// signal, but leaves status unchanged (enriching) — so an article interrupted
	// mid-enrichment is still re-selected by ListWork and resumed. The final step
	// uses SaveEnrichment to flip status to enriched. Returns ErrNotFound if the
	// article was deleted in the meantime.
	SaveEnrichmentProgress(ctx context.Context, id string, e model.Enrichment) error

	// ListWork returns up to `limit` articles awaiting pipeline work (any of
	// model.WorkStatuses), oldest first, for the worker to drain.
	ListWork(ctx context.Context, limit int) ([]model.Article, error)

	// UpsertProgress applies reading progress with LWW semantics on UpdatedAt.
	// It returns applied=true when the incoming record won (was stored) and
	// applied=false when an existing record had a newer-or-equal UpdatedAt.
	UpsertProgress(ctx context.Context, p model.Progress) (applied bool, err error)

	// ListProgress returns progress records updated at or after `since`. Pass
	// the zero time for everything.
	ListProgress(ctx context.Context, since time.Time) ([]model.Progress, error)

	// RetryArticle resets a failed article to the queue state for the stage that
	// failed (fetch_failed → queued, enrich_failed → fetched) so the worker
	// resumes from there, clearing error. Returns ErrNotFound if the article
	// does not exist.
	RetryArticle(ctx context.Context, id string) error

	// ReEnrich queues an already-enriched article for re-enrichment. mode
	// model.ReEnrichModeFull resets status to fetched (re-translate the whole
	// article, keeping the fetched content); mode model.ReEnrichModeTopup sets
	// status to topup_queued and keeps the existing enrichment blob so the worker
	// can merge in only the missing spans. Returns ErrNotFound if the article
	// does not exist.
	ReEnrich(ctx context.Context, id, mode string) error

	// SetPinned sets the article's pinned flag (a user library flag) and bumps
	// UpdatedAt so the change is carried by the next delta sync. Returns
	// ErrNotFound if the article does not exist.
	SetPinned(ctx context.Context, id string, pinned bool) error

	// MarkdownUnitsUsedToday returns the number of markdown.new request units
	// consumed during the current UTC day, for surfacing remaining budget. It
	// returns 0 when no units have been spent today.
	MarkdownUnitsUsedToday(ctx context.Context) (int, error)

	// TryConsumeMarkdownUnits atomically reserves cost units against the current
	// UTC day, succeeding only if doing so keeps the day's total at or below
	// dailyLimit. A dailyLimit <= 0 means unlimited and always succeeds. It
	// reports whether the reservation was applied and the day's total afterwards
	// (unchanged when allowed is false).
	TryConsumeMarkdownUnits(ctx context.Context, cost, dailyLimit int) (allowed bool, usedAfter int, err error)

	// RefundMarkdownUnits returns cost previously-reserved units to the current
	// day's budget (e.g. when a markdown.new call ultimately failed and the work
	// fell back to local extraction). It never drives the counter below zero.
	RefundMarkdownUnits(ctx context.Context, cost int) error
}

// LLMClient is the boundary to the OpenAI-compatible provider. Concrete
// constructor: llm.New(cfg) *llm.Client.
type LLMClient interface {
	// Enrich performs a single enrichment call for the given article at the
	// supplied settings and enrichment version, returning the validated
	// enrichment payload and provider usage. It performs one attempt; retry and
	// backoff are the caller's (enrich.Pool's) responsibility.
	Enrich(ctx context.Context, a *model.Article, settings model.Settings, enrichmentVersion int) (*model.Enrichment, Usage, error)

	// EnrichSpans performs a single incremental ("top up") enrichment call: it
	// annotates only the supplied token spans (the ranges left uncovered by the
	// current sentence translations), leaving the rest of the article untouched.
	// The caller merges the returned partial enrichment into the existing one.
	// Like Enrich, it performs exactly one attempt.
	EnrichSpans(ctx context.Context, a *model.Article, settings model.Settings, enrichmentVersion int, spans []model.Span) (*model.Enrichment, Usage, error)

	// Summarize produces a short abstract of the article in the user's target
	// language. It is the first step of the step-wise enrichment; the resulting
	// summary is persisted and fed back as context into the per-chunk enrichment
	// calls. Like Enrich, it performs exactly one attempt.
	Summarize(ctx context.Context, a *model.Article, settings model.Settings) (string, Usage, error)

	// Normalize runs the content-normalization pass of the fetch stage: it strips
	// leftover navigation / chrome / boilerplate from the extracted article text
	// and returns the cleaned body. It runs after extraction and before
	// tokenization so the tokens — and therefore every downstream enrichment span
	// — are computed against the cleaned text. The implementation must fail open
	// (return the original text) when the model over-deletes or returns empty, so
	// a bad pass never destroys the article. Like Enrich, it performs exactly one
	// attempt.
	Normalize(ctx context.Context, title, text string, settings model.Settings) (string, Usage, error)
}

// Usage is the provider token-accounting for a single LLM call, logged for cost
// monitoring. Zero values are acceptable if the provider omits usage.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Extractor is the article fetch+extract boundary. Concrete constructor:
// extract.New(cfg) *extract.Extractor.
type Extractor interface {
	// Extract fetches rawURL (with SSRF guards and timeout) and returns the
	// readability result. The returned CanonicalURL is what ingestion hashes
	// for dedup.
	Extract(ctx context.Context, rawURL string) (*ExtractResult, error)
}

// ExtractResult is the output of extraction: canonicalized URL plus the
// readability-extracted content and metadata.
type ExtractResult struct {
	CanonicalURL string
	Title        string
	Author       string
	Domain       string
	Lang         string
	HTML         string
	Text         string
}

// ContentUpdate carries the extracted content the worker writes back to an
// article after a successful fetch stage (see Store.SaveContent).
type ContentUpdate struct {
	SourceURL    string
	Title        string
	Author       string
	SourceDomain string
	Lang         string
	Text         string
	Tokens       []model.Token
}

// Ingestor orchestrates the ingestion pipeline. Concrete constructor:
// ingest.New(cfg, store, worker) *ingest.Ingestor.
type Ingestor interface {
	// Add ingests rawURL: normalize, dedup, persist as queued, notify the
	// worker. Fetch and enrichment happen asynchronously in the worker. On a
	// dedup hit it returns the existing article without re-spending. The
	// returned article carries its current status.
	Add(ctx context.Context, rawURL string) (*model.Article, error)

	// Retry resumes a failed article from the stage that failed (re-fetch or
	// re-enrich) and notifies the worker. Returns ErrNotFound for an unknown id.
	Retry(ctx context.Context, id string) error

	// ReEnrich re-runs enrichment for an already-enriched article and notifies
	// the worker. mode is model.ReEnrichModeFull (re-translate everything) or
	// model.ReEnrichModeTopup (fill only the uncovered spans). Returns
	// ErrNotFound for an unknown id.
	ReEnrich(ctx context.Context, id, mode string) error
}

// EnrichmentWorker is the async enrichment worker pool. Concrete constructor:
// enrich.NewPool(cfg, store, llm) *enrich.Pool.
type EnrichmentWorker interface {
	// Start launches the worker pool and blocks until ctx is cancelled (run it
	// in its own goroutine). It drains pending articles and reacts to Notify.
	Start(ctx context.Context)

	// Notify signals that new pending work may be available (e.g. after an
	// ingest or reenrich). Non-blocking and safe to call from any goroutine; a
	// no-op if a wake is already queued.
	Notify()
}
