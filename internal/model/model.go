// Package model holds the shared domain types for Deep Reader.
//
// These types are the single contract shared across the storage, ingestion,
// enrichment, LLM and HTTP layers, and they mirror the API / IndexedDB shapes
// described in the architecture spec (§8 data model, §9 API). JSON tags are
// load-bearing: they are the wire format consumed by the PWA client, so do not
// rename them without updating the frontend contract.
package model

import (
	"fmt"
	"strings"
	"time"
)

// Article status values stored in the articles.status column. They form an
// explicit two-stage pipeline — fetch (extract original content) then enrich
// (LLM annotation) — so the UI can show exactly which stage an article is in
// and, on failure, which stage failed. The in-flight states (StatusFetching,
// StatusEnriching) are persisted for the UI; a crash mid-stage is safe because
// the worker re-selects them as their preceding queue state (see ListWork).
const (
	// StatusQueued means the article record exists but its original content has
	// not been fetched yet. This is the state right after ingestion.
	StatusQueued = "queued"
	// StatusFetching means the fetch/extract stage is in flight.
	StatusFetching = "fetching"
	// StatusFetched means the original content has been fetched and tokenized
	// and the article is waiting for the enrichment stage. UI label: original
	// content received.
	StatusFetched = "fetched"
	// StatusEnriching means the enrichment (LLM) stage is in flight. UI label:
	// sent for processing.
	StatusEnriching = "enriching"
	// StatusEnriched means enrichment completed successfully and the payload is
	// available. This is the terminal success state. UI label: ready.
	StatusEnriched = "enriched"
	// StatusFetchFailed means the fetch stage failed; the reason is stored in
	// Article.Error. Retry re-runs the fetch stage.
	StatusFetchFailed = "fetch_failed"
	// StatusEnrichFailed means the enrichment stage failed after exhausting
	// retries; the reason is stored in Article.Error. Retry re-runs only the
	// enrichment stage (the fetched content is preserved).
	StatusEnrichFailed = "enrich_failed"
	// StatusTopupQueued marks an already-enriched article that the user asked to
	// "top up": only the token spans not yet covered by any sentence translation
	// are re-sent to the LLM and the result is merged into the existing
	// enrichment (see enrich.Pool.runTopUp). The existing enrichment blob is
	// preserved until the merge completes.
	StatusTopupQueued = "topup_queued"
	// StatusBlocked means the fetch succeeded but the retrieved content was
	// recognised as a bot-verification / captcha interstitial (e.g. Cloudflare,
	// Vercel Security Checkpoint) rather than the real article. It is detected in
	// the fetch stage before any LLM call, so no tokens are spent annotating a
	// challenge page. The reason is stored in Article.Error. It is terminal (the
	// worker does not pick it up); a manual retry re-runs the fetch stage in case
	// the wall has since cleared.
	StatusBlocked = "blocked"
)

// WorkStatuses is the set of statuses the enrichment worker picks up. It
// includes the in-flight states so a stuck article (server crashed mid-stage)
// is re-processed: queued/fetching need fetch, fetched/enriching need enrich;
// topup_queued needs an incremental (gap-filling) enrich.
var WorkStatuses = []string{StatusQueued, StatusFetching, StatusFetched, StatusEnriching, StatusTopupQueued}

// Re-enrichment modes for the user-triggered "improve translation" action (see
// ports.Store.ReEnrich). Full re-translates the whole article; Topup fills only
// the spans no sentence covers yet.
const (
	ReEnrichModeFull  = "full"
	ReEnrichModeTopup = "topup"
)

// CEFR proficiency levels. These are the legal values for Settings.CEFRLevel
// and Settings.MinDifficultyToHighlight, and for DifficultWord.CEFRLevel.
const (
	CEFRA2 = "A2"
	CEFRB1 = "B1"
	CEFRB2 = "B2"
	CEFRC1 = "C1"
	CEFRC2 = "C2"
)

// CEFRLevels is the ordered set of valid CEFR levels (ascending difficulty).
var CEFRLevels = []string{CEFRA2, CEFRB1, CEFRB2, CEFRC1, CEFRC2}

// Phrase types for Phrase.Type. A phrase is a contiguous token range that the
// reader UI treats as a single translatable unit.
const (
	// PhraseTypeIdiom is a non-compositional multi-word expression.
	PhraseTypeIdiom = "idiom"
	// PhraseTypePhrasalVerb is a verb + particle combination.
	PhraseTypePhrasalVerb = "phrasal_verb"
	// PhraseTypeTerm is a domain-specific term worth defining.
	PhraseTypeTerm = "term"
)

// PhraseTypes is the set of valid Phrase.Type values.
var PhraseTypes = []string{PhraseTypeIdiom, PhraseTypePhrasalVerb, PhraseTypeTerm}

// DefaultTargetLanguage is the MVP translation target. The schema keeps the
// field configurable, but the MVP hard-defaults to Russian.
const DefaultTargetLanguage = "ru"

// DefaultMarkdownWarnThreshold is the seeded value for Settings.MarkdownWarnThreshold:
// warn once today's remaining markdown.new conversions drop to this count or below.
// Must match the DEFAULT in migration 00006.
const DefaultMarkdownWarnThreshold = 5

// MaxMarkdownWarnThreshold is the accepted upper bound for the warn threshold.
const MaxMarkdownWarnThreshold = 100

// MaxEnrichmentPromptLen is the accepted upper bound (in bytes) for a custom
// enrichment prompt template submitted via PATCH /api/settings.
const MaxEnrichmentPromptLen = 8000

// MaxBotWallSignaturesLen is the accepted upper bound (in bytes) for the custom
// bot-wall signature list submitted via PATCH /api/settings.
const MaxBotWallSignaturesLen = 4000

// Chunk-size bounds for Settings.ChunkTokens, the user-tunable step-wise
// enrichment window. A value of 0 means "use the deployment default"
// (config.LLMChunkTokens); any non-zero value must fall within [Min, Max].
// The lower bound keeps a chunk large enough to hold a sentence or two; the
// upper bound keeps each completion short enough to avoid truncation.
const (
	MinChunkTokens = 50
	MaxChunkTokens = 2000
)

// Reader font-size presets for Settings.FontSize. These map (client-side) to
// concrete rem values; the backend only stores/validates the enum.
const (
	FontSizeS  = "s"
	FontSizeM  = "m"
	FontSizeL  = "l"
	FontSizeXL = "xl"
)

// FontSizes is the set of valid Settings.FontSize values. DefaultFontSize
// reproduces the previously hard-coded reader size and must match migration 00016.
var FontSizes = []string{FontSizeS, FontSizeM, FontSizeL, FontSizeXL}

// DefaultFontSize is the seeded value for Settings.FontSize.
const DefaultFontSize = FontSizeM

// Reader line-height presets for Settings.LineHeight. These map (client-side)
// to concrete unitless multipliers; the backend only stores/validates the enum.
const (
	LineHeightCompact = "compact"
	LineHeightNormal  = "normal"
	LineHeightRelaxed = "relaxed"
)

// LineHeights is the set of valid Settings.LineHeight values. DefaultLineHeight
// reproduces the previously hard-coded reader spacing and must match migration 00016.
var LineHeights = []string{LineHeightCompact, LineHeightNormal, LineHeightRelaxed}

// DefaultLineHeight is the seeded value for Settings.LineHeight.
const DefaultLineHeight = LineHeightNormal

// DefaultBotWallSignatures is the built-in set of lowercased substrings that
// flag a bot-verification / captcha interstitial (Cloudflare, Vercel Security
// Checkpoint, …) returned in place of the real article. The fetch stage matches
// content against these — combined with a short-body guard — before any LLM call
// so no tokens are spent annotating a challenge page. Users can override the list
// via Settings.BotWallSignatures; an empty override falls back to this set.
var DefaultBotWallSignatures = []string{
	"vercel security checkpoint",
	"we're verifying your browser",
	"website owner? click here to fix",
	"just a moment...",
	"checking your browser before accessing",
	"checking if the site connection is secure",
	"attention required! | cloudflare",
	"enable javascript and cookies to continue",
	"verify you are human",
	"verifying you are human",
	"please verify you are a human",
	"complete the security check to access",
	"ddos protection by cloudflare",
	"performance & security by cloudflare",
	"needs to review the security of your connection",
}

// ParseBotWallSignatures splits a newline-separated signature list into the
// normalised (trimmed, lowercased, non-empty) substrings used for matching. The
// stored format is one signature per line so it is easy to edit in a textarea.
func ParseBotWallSignatures(text string) []string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		sig := strings.ToLower(strings.TrimSpace(line))
		if sig != "" {
			out = append(out, sig)
		}
	}
	return out
}

// Token is a single deterministic token produced by the tokenizer. Start and
// End are byte offsets into Article.OriginalText such that
// OriginalText[Start:End] == Text. Index is the token's position in the token
// slice and is the identifier used by enrichment references (TokenIndex,
// StartIndex/EndIndex).
type Token struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// Article is the full server-side record: metadata, the extracted original
// text, the deterministic tokenization, and enrichment lifecycle fields. It is
// the source of truth; the client mirrors a subset of it.
type Article struct {
	ID           string  `json:"id"`
	SourceURL    string  `json:"source_url"`
	URLHash      string  `json:"url_hash"`
	Title        string  `json:"title"`
	Author       string  `json:"author"`
	SourceDomain string  `json:"source_domain"`
	Lang         string  `json:"lang"`
	OriginalText string  `json:"original_text"`
	Tokens       []Token `json:"tokens"`
	// Summary is a short LLM-produced abstract of the article, generated as the
	// first step of the step-wise enrichment. Empty until summarized.
	Summary           string `json:"summary,omitempty"`
	Status            string `json:"status"`
	EnrichmentVersion int    `json:"enrichment_version"`
	Error             string `json:"error,omitempty"`
	// LLMModel is the effective model name that produced the enrichment, stamped
	// when the article is flipped to status=enriched. Empty until enriched.
	LLMModel string `json:"llm_model,omitempty"`
	// Pinned is a user flag keeping the article at the top of the library. It is
	// synced as ordinary metadata (toggling it bumps UpdatedAt).
	Pinned     bool      `json:"pinned"`
	CreatedAt  time.Time `json:"created_at"`
	EnrichedAt time.Time `json:"enriched_at,omitzero"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Enrichment is the LLM-produced annotation layer for an article. All token
// references are indices into Article.Tokens.
type Enrichment struct {
	DifficultWords []DifficultWord `json:"difficult_words"`
	Phrases        []Phrase        `json:"phrases"`
	Sentences      []Sentence      `json:"sentences"`
	Glossary       []GlossaryItem  `json:"glossary"`
}

// TranslationSourceGlossary marks a DifficultWord whose translation was recovered
// from the article glossary because the model echoed the source word instead of
// translating it. An empty Source means the translation came straight from the
// model.
const TranslationSourceGlossary = "glossary"

// DifficultWord is a single token that is above the user's CEFR level, with its
// contextual translation. TokenIndex references Article.Tokens.
type DifficultWord struct {
	TokenIndex  int    `json:"token_index"`
	Lemma       string `json:"lemma"`
	Translation string `json:"translation"`
	CEFRLevel   string `json:"cefr_level"`
	// Source records where Translation came from. Empty for a model-supplied
	// translation; TranslationSourceGlossary when recovered from the glossary.
	Source string `json:"source,omitempty"`
}

// Phrase is a contiguous token range [StartIndex, EndIndex] (inclusive) that
// forms an idiom, phrasal verb, or domain term, with its translation or
// definition.
//
// Text is the literal phrase the LLM claims to annotate, echoed back from the
// source. It is the validation anchor: the enrichment pipeline rejects a phrase
// whose Text does not match the words actually spanned by [StartIndex, EndIndex]
// (see enrich.validateEnrichment). This catches the common failure mode where
// the model picks a correct phrase but emits an over-wide or drifted token
// range, which would otherwise surface in the reader as a term tooltip showing
// a whole clause instead of the term.
type Phrase struct {
	StartIndex  int    `json:"start_index"`
	EndIndex    int    `json:"end_index"`
	Type        string `json:"type"`
	Text        string `json:"text"`
	Translation string `json:"translation"`
}

// Sentence is a contiguous token range [StartIndex, EndIndex] (inclusive) with
// a full-sentence translation, surfaced on long-tap / selection.
type Sentence struct {
	StartIndex  int    `json:"start_index"`
	EndIndex    int    `json:"end_index"`
	Translation string `json:"translation"`
}

// GlossaryItem is a domain term with a definition (as opposed to a plain
// translation) that the LLM deemed worth explaining.
type GlossaryItem struct {
	Term       string `json:"term"`
	Definition string `json:"definition"`
}

// Span is a contiguous, inclusive range of token indices [Start, End]. It is
// used to drive incremental ("top up") enrichment: the spans of tokens left
// uncovered by the current sentence translations are the only ranges re-sent to
// the LLM (see enrich.uncoveredSpans and llm.Client.EnrichSpans).
type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// User is the single built-in account. PasswordHash is a bcrypt hash and is
// never serialised to the client (no JSON tags are sent over the wire for it;
// the type is server-side only).
type User struct {
	Username     string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Settings is the singleton user-settings row. Persisted in the DB (not env),
// since these are user-tunable.
type Settings struct {
	CEFRLevel                string `json:"cefr_level"`
	TargetLanguage           string `json:"target_language"`
	LLMModel                 string `json:"llm_model"`
	MinDifficultyToHighlight string `json:"min_difficulty_to_highlight"`
	// MarkdownWarnThreshold is the remaining-conversions count at or below which
	// the client shows a low-budget warning banner. 0 disables the warning.
	MarkdownWarnThreshold int `json:"markdown_warn_threshold"`
	// EnrichmentPrompt is the user's custom enrichment system-prompt template.
	// Empty means use the built-in default (llm.DefaultEnrichmentPromptTemplate).
	EnrichmentPrompt string `json:"enrichment_prompt"`
	// SummaryPrompt is the user's custom summary system-prompt template (the
	// first step of the step-wise enrichment). Empty means use the built-in
	// default (llm.DefaultSummaryPromptTemplate).
	SummaryPrompt string `json:"summary_prompt"`
	// NormalizePrompt is the user's custom content-normalization system-prompt
	// template. The normalization step runs in the fetch stage (after extraction,
	// before tokenization) and strips navigation / chrome / boilerplate the
	// extractor leaked into the article body. Empty means use the built-in
	// default (normalize.DefaultPromptTemplate).
	NormalizePrompt string `json:"normalize_prompt"`
	// BotWallSignatures is the user's custom newline-separated list of bot-wall /
	// captcha substrings the fetch stage matches against to detect a challenge
	// page before any LLM call. Empty means use the built-in
	// DefaultBotWallSignatures.
	BotWallSignatures string `json:"bot_wall_signatures"`
	// ChunkTokens is the user-tunable step-wise enrichment window size (target
	// tokens per per-chunk LLM call). 0 means "use the deployment default"
	// (config.LLMChunkTokens); a non-zero value must be within
	// [MinChunkTokens, MaxChunkTokens]. Smaller chunks keep each completion
	// shorter (less truncation risk) at the cost of more requests per article.
	ChunkTokens int `json:"chunk_tokens"`
	// FontSize is the reader text-size preset (FontSize* constants). It controls
	// only presentation in the article reader and syncs across devices.
	FontSize string `json:"font_size"`
	// LineHeight is the reader line-spacing preset (LineHeight* constants). It
	// controls only presentation in the article reader and syncs across devices.
	LineHeight string    `json:"line_height"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// SettingsPatch is a partial update of Settings for PATCH /api/settings. Nil
// fields are left unchanged; non-nil fields are applied. UpdatedAt is set by
// the store on apply.
type SettingsPatch struct {
	CEFRLevel                *string `json:"cefr_level,omitempty"`
	TargetLanguage           *string `json:"target_language,omitempty"`
	LLMModel                 *string `json:"llm_model,omitempty"`
	MinDifficultyToHighlight *string `json:"min_difficulty_to_highlight,omitempty"`
	MarkdownWarnThreshold    *int    `json:"markdown_warn_threshold,omitempty"`
	EnrichmentPrompt         *string `json:"enrichment_prompt,omitempty"`
	SummaryPrompt            *string `json:"summary_prompt,omitempty"`
	NormalizePrompt          *string `json:"normalize_prompt,omitempty"`
	BotWallSignatures        *string `json:"bot_wall_signatures,omitempty"`
	ChunkTokens              *int    `json:"chunk_tokens,omitempty"`
	FontSize                 *string `json:"font_size,omitempty"`
	LineHeight               *string `json:"line_height,omitempty"`
}

// LLMProvider is a user-managed LLM connection profile (Settings > LLM,
// backend-only). The active profile (IsActive) supplies the base URL, API key,
// and model for every LLM call at request time. APIKey is the secret: it is
// persisted as-is but never serialized to the client — the API returns
// LLMProviderView with a masked preview instead. Exactly one profile is active.
type LLMProvider struct {
	ID       string
	Name     string
	BaseURL  string
	APIKey   string
	Model    string
	IsActive bool
	// ForceJSONObject makes every LLM call for this profile use the json_object
	// response format directly, skipping json_schema and its one-shot fallback.
	// Enable it for providers that always reject json_schema (e.g. an OpenRouter
	// model whose data-policy/guardrail leaves no structured-outputs endpoint).
	ForceJSONObject bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// LLMProviderView is the client-facing projection of an LLMProvider. It carries
// a masked key preview and a HasKey flag instead of the raw secret, so the API
// can list profiles without ever exposing the stored key.
type LLMProviderView struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	BaseURL         string    `json:"base_url"`
	Model           string    `json:"model"`
	IsActive        bool      `json:"is_active"`
	ForceJSONObject bool      `json:"force_json_object"`
	HasKey          bool      `json:"has_key"`
	KeyPreview      string    `json:"key_preview"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// View returns the masked, client-safe projection of the provider.
func (p LLMProvider) View() LLMProviderView {
	return LLMProviderView{
		ID:              p.ID,
		Name:            p.Name,
		BaseURL:         p.BaseURL,
		Model:           p.Model,
		IsActive:        p.IsActive,
		ForceJSONObject: p.ForceJSONObject,
		HasKey:          p.APIKey != "",
		KeyPreview:      MaskSecret(p.APIKey),
		CreatedAt:       p.CreatedAt,
		UpdatedAt:       p.UpdatedAt,
	}
}

// LLMProviderInput is the create/update payload for a provider. On update a nil
// APIKey leaves the stored key unchanged (the secret is write-only); a non-nil
// value replaces it (an empty string clears it). On create a nil APIKey means
// no key (valid for keyless local providers such as Ollama).
type LLMProviderInput struct {
	Name            string  `json:"name"`
	BaseURL         string  `json:"base_url"`
	Model           string  `json:"model"`
	ForceJSONObject bool    `json:"force_json_object"`
	APIKey          *string `json:"api_key,omitempty"`
}

// Validate checks the non-secret fields required for a usable connection. It is
// called for both create and update; the API maps a non-nil result to 400.
func (in LLMProviderInput) Validate() error {
	if strings.TrimSpace(in.Name) == "" {
		return fmt.Errorf("name is required")
	}
	base := strings.TrimSpace(in.BaseURL)
	if base == "" {
		return fmt.Errorf("base_url is required")
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return fmt.Errorf("base_url must start with http:// or https://")
	}
	if strings.TrimSpace(in.Model) == "" {
		return fmt.Errorf("model is required")
	}
	return nil
}

// MaskSecret returns a display-safe preview of a secret: bullets followed by the
// last 4 characters, or "" when empty. Secrets of 4 characters or fewer are
// fully masked so no meaningful prefix leaks.
func MaskSecret(s string) string {
	if s == "" {
		return ""
	}
	const visible = 4
	if len(s) <= visible {
		return strings.Repeat("•", len(s))
	}
	return "••••" + s[len(s)-visible:]
}

// Progress is the reading progress for a single article. It is synced with LWW
// semantics keyed on UpdatedAt.
type Progress struct {
	ArticleID string    `json:"article_id"`
	Position  int       `json:"position"`
	IsRead    bool      `json:"is_read"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ArticleMeta is the library-listing projection of an Article returned by
// GET /api/config. It deliberately omits the heavy original_text / tokens /
// enrichment payload.
type ArticleMeta struct {
	ID           string `json:"id"`
	SourceURL    string `json:"source_url"`
	Title        string `json:"title"`
	Author       string `json:"author"`
	SourceDomain string `json:"source_domain"`
	Status       string `json:"status"`
	// Pinned keeps the article at the top of the library (see Article.Pinned).
	Pinned            bool      `json:"pinned"`
	CreatedAt         time.Time `json:"created_at"`
	EnrichedAt        time.Time `json:"enriched_at,omitzero"`
	EnrichmentVersion int       `json:"enrichment_version"`
	TokenCount        int       `json:"token_count"`
	// EnrichmentCoverage is the fraction [0,1] of tokens covered by at least one
	// sentence translation — the completeness signal the UI shows so the reader
	// can tell when the LLM stopped annotating partway through. Zero until the
	// article is enriched. See store.sentenceCoverage for the definition.
	EnrichmentCoverage float64 `json:"enrichment_coverage"`
	// Summary is the short LLM-produced abstract shown as a quick preview on the
	// library card. Empty until the article has been summarized.
	Summary string `json:"summary,omitempty"`
	// LLMModel is the model name that enriched the article (see Article.LLMModel).
	// Empty until enriched.
	LLMModel string `json:"llm_model,omitempty"`
	// ProgressStage is a short, human-readable label of the pipeline step the
	// article is currently in (e.g. "Fetching content", "Translating (3/5)"),
	// set by the enrichment worker. Empty for articles at rest (queued, enriched,
	// or failed). The UI shows it during processing.
	ProgressStage string `json:"progress_stage,omitempty"`
}

// ArticlePayload is the full enriched-article response from
// GET /api/articles/:id. Enrichment may be nil if the article is not yet
// enriched (in which case the handler typically returns 409, but Status
// communicates the state either way).
type ArticlePayload struct {
	ID           string  `json:"id"`
	Status       string  `json:"status"`
	Title        string  `json:"title"`
	Author       string  `json:"author"`
	Lang         string  `json:"lang"`
	OriginalText string  `json:"original_text"`
	Tokens       []Token `json:"tokens"`
	// Summary is the article abstract produced by the first enrichment step,
	// shown in the reader. Empty until summarized.
	Summary           string      `json:"summary,omitempty"`
	Enrichment        *Enrichment `json:"enrichment,omitempty"`
	EnrichmentVersion int         `json:"enrichment_version"`
	// EnrichmentCoverage mirrors ArticleMeta.EnrichmentCoverage so the reader
	// page header can show completeness without a separate library lookup.
	EnrichmentCoverage float64 `json:"enrichment_coverage"`
	// LLMModel mirrors ArticleMeta.LLMModel so the reader can show which model
	// produced the enrichment. Empty until enriched.
	LLMModel string `json:"llm_model,omitempty"`
	// ProgressStage mirrors ArticleMeta.ProgressStage so the reader's
	// processing screen can show the current pipeline step. Empty when the
	// article is at rest.
	ProgressStage string `json:"progress_stage,omitempty"`
}

// ArticleRaw is the raw LLM output captured when the enrichment stage failed to
// decode the provider's response, returned by GET /api/articles/:id/raw so the
// UI can show the unparsed answer for inspection. Raw is empty when nothing was
// captured (e.g. a fetch failure, a network/HTTP error, or a successful enrich).
type ArticleRaw struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	// Error is the failure message stored on the article (Article.Error).
	Error string `json:"error,omitempty"`
	// Raw is the verbatim model output that failed to decode.
	Raw string `json:"raw"`
}

// ServerInfo holds the non-secret deployment configuration sent to the client
// on bootstrap. Secrets (LLM_API_KEY, password hashes) are intentionally omitted.
type ServerInfo struct {
	HTTPPort     int    `json:"http_port"`
	DatabasePath string `json:"database_path"`

	TrustProxy     bool     `json:"trust_proxy"`
	TrustedProxies []string `json:"trusted_proxies"`

	LoginMaxAttempts     int    `json:"login_max_attempts"`
	LoginAttemptWindow   string `json:"login_attempt_window"`
	LoginLockoutDuration string `json:"login_lockout_duration"`

	LLMAPIBaseURL     string `json:"llm_api_base_url"`
	LLMModel          string `json:"llm_model"`
	LLMMaxConcurrent  int    `json:"llm_max_concurrent"`
	LLMRequestTimeout string `json:"llm_request_timeout"`
	LLMMaxRetries     int    `json:"llm_max_retries"`
	LLMChunkTokens    int    `json:"llm_chunk_tokens"`

	ReadabilityTimeout string `json:"readability_timeout"`
	EnrichmentVersion  int    `json:"enrichment_version"`
	// EnrichmentPromptDefault is the built-in enrichment prompt template the
	// client pre-fills the editor with and resets to. It is non-secret.
	EnrichmentPromptDefault string `json:"enrichment_prompt_default"`
	// SummaryPromptDefault is the built-in summary prompt template the client
	// pre-fills the editor with and resets to. It is non-secret.
	SummaryPromptDefault string `json:"summary_prompt_default"`
	// NormalizePromptDefault is the built-in content-normalization prompt template
	// the client pre-fills the editor with and resets to. It is non-secret.
	NormalizePromptDefault string `json:"normalize_prompt_default"`
	// BotWallSignaturesDefault is the built-in newline-separated bot-wall /
	// captcha signature list the client pre-fills the editor with and resets to.
	BotWallSignaturesDefault string `json:"bot_wall_signatures_default"`

	MarkdownEnabled        bool   `json:"markdown_enabled"`
	MarkdownBaseURL        string `json:"markdown_base_url"`
	MarkdownTimeout        string `json:"markdown_timeout"`
	MarkdownDailyLimit     int    `json:"markdown_daily_limit"`
	MarkdownCostPerArticle int    `json:"markdown_cost_per_article"`

	LogLevel  string `json:"log_level"`
	LogFormat string `json:"log_format"`

	// SentryDSN and SentryFrontendDSN are MASKED for display in the settings UI —
	// the raw values never leave the server. SentryEnvironment is shown verbatim.
	// Empty strings mean the variable is unset. (The functional, unmasked browser
	// DSN is delivered separately via ConfigResponse.Sentry.)
	SentryDSN         string `json:"sentry_dsn"`
	SentryFrontendDSN string `json:"sentry_frontend_dsn"`
	SentryEnvironment string `json:"sentry_environment"`

	Version string `json:"version"`
}

// AuthStatus is the authentication state the client needs to route the user:
// to /setup when the service is not yet initialized, to /login when it is but
// the request is unauthenticated, or into the app when authenticated. It is
// carried by GET /api/config (the only endpoint reachable before login).
type AuthStatus struct {
	// Initialized is true once the single built-in account has been created.
	Initialized bool `json:"initialized"`
	// Authenticated is true when the request carried a valid session token.
	Authenticated bool `json:"authenticated"`
}

// ConfigResponse is the single bootstrap/delta-sync response from
// GET /api/config. ServerTime is the authoritative clock the client should use
// as the next sync cursor.
//
// Auth is always populated. When the caller is not authenticated (or the service
// is not initialized) the heavy library fields are left empty — the client only
// needs Auth to decide where to route — so unauthenticated callers never receive
// library data.
type ConfigResponse struct {
	Auth           AuthStatus     `json:"auth"`
	Settings       Settings       `json:"settings"`
	Articles       []ArticleMeta  `json:"articles"`
	Progress       []Progress     `json:"progress"`
	MarkdownBudget MarkdownBudget `json:"markdown_budget"`
	ServerInfo     ServerInfo     `json:"server_info"`
	// Sentry carries the non-secret browser Sentry configuration. It is present
	// even for unauthenticated callers so error reporting works on the /login and
	// /setup pages; DSN is empty when frontend reporting is disabled.
	Sentry     SentryConfig `json:"sentry"`
	ServerTime time.Time    `json:"server_time"`
}

// SentryConfig is the browser Sentry configuration delivered to the client on
// bootstrap. The DSN is public by design (browser SDKs embed it), so this
// carries no secret. An empty DSN means frontend error reporting is disabled.
type SentryConfig struct {
	DSN         string `json:"dsn"`
	Environment string `json:"environment"`
	Release     string `json:"release"`
}

// SetupRequest is the POST /api/setup body: the credentials for the single
// built-in account, set once during first-run initialization.
type SetupRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginRequest is the POST /api/login body.
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AuthResponse is returned by POST /api/setup and POST /api/login. Token is the
// opaque session bearer token the client stores and sends as Authorization:
// Bearer on subsequent requests.
type AuthResponse struct {
	Token    string `json:"token"`
	Username string `json:"username"`
}

// MarkdownBudget reports the markdown.new daily request-unit budget so the
// client can show remaining capacity and warn before it runs out. It is part of
// ConfigResponse. When markdown.new extraction is disabled, Enabled is false and
// the remaining fields are zero.
type MarkdownBudget struct {
	// Enabled is true when markdown.new is the active primary extractor.
	Enabled bool `json:"enabled"`
	// DailyLimit is the request-unit budget per UTC day (0 means unlimited).
	DailyLimit int `json:"daily_limit"`
	// CostPerArticle is the request units one conversion consumes.
	CostPerArticle int `json:"cost_per_article"`
	// UnitsUsed is the request units consumed so far today.
	UnitsUsed int `json:"units_used"`
	// UnitsRemaining is DailyLimit - UnitsUsed, clamped at zero.
	UnitsRemaining int `json:"units_remaining"`
	// ArticlesRemaining is how many more conversions today's budget allows
	// (UnitsRemaining / CostPerArticle).
	ArticlesRemaining int `json:"articles_remaining"`
}

// AddArticleRequest is the POST /api/articles body. Either URL (fetch an
// article from the web) or Text (ingest pasted raw content directly) is
// provided. When Text is set, Title is an optional human-friendly heading.
type AddArticleRequest struct {
	URL   string `json:"url"`
	Text  string `json:"text"`
	Title string `json:"title"`
}

// AddArticleResponse is the POST /api/articles response. Deduplication is
// transparent: a duplicate URL returns the existing article's id/status.
type AddArticleResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// ReEnrichRequest is the POST /api/articles/:id/reenrich body. Mode selects a
// full re-translate (ReEnrichModeFull) or an incremental gap fill
// (ReEnrichModeTopup).
type ReEnrichRequest struct {
	Mode string `json:"mode"`
}
