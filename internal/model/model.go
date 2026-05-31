// Package model holds the shared domain types for Deep Reader.
//
// These types are the single contract shared across the storage, ingestion,
// enrichment, LLM and HTTP layers, and they mirror the API / IndexedDB shapes
// described in the architecture spec (§8 data model, §9 API). JSON tags are
// load-bearing: they are the wire format consumed by the PWA client, so do not
// rename them without updating the frontend contract.
package model

import "time"

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
)

// WorkStatuses is the set of statuses the enrichment worker picks up. It
// includes the in-flight states so a stuck article (server crashed mid-stage)
// is re-processed: queued/fetching need fetch, fetched/enriching need enrich.
var WorkStatuses = []string{StatusQueued, StatusFetching, StatusFetched, StatusEnriching}

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
	ID                string    `json:"id"`
	SourceURL         string    `json:"source_url"`
	URLHash           string    `json:"url_hash"`
	Title             string    `json:"title"`
	Author            string    `json:"author"`
	SourceDomain      string    `json:"source_domain"`
	Lang              string    `json:"lang"`
	OriginalText      string    `json:"original_text"`
	Tokens            []Token   `json:"tokens"`
	Status            string    `json:"status"`
	EnrichmentVersion int       `json:"enrichment_version"`
	Error             string    `json:"error,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	EnrichedAt        time.Time `json:"enriched_at,omitzero"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// Enrichment is the LLM-produced annotation layer for an article. All token
// references are indices into Article.Tokens.
type Enrichment struct {
	DifficultWords []DifficultWord `json:"difficult_words"`
	Phrases        []Phrase        `json:"phrases"`
	Sentences      []Sentence      `json:"sentences"`
	Glossary       []GlossaryItem  `json:"glossary"`
}

// DifficultWord is a single token that is above the user's CEFR level, with its
// contextual translation. TokenIndex references Article.Tokens.
type DifficultWord struct {
	TokenIndex  int    `json:"token_index"`
	Lemma       string `json:"lemma"`
	Translation string `json:"translation"`
	CEFRLevel   string `json:"cefr_level"`
}

// Phrase is a contiguous token range [StartIndex, EndIndex] (inclusive) that
// forms an idiom, phrasal verb, or domain term, with its translation or
// definition.
type Phrase struct {
	StartIndex  int    `json:"start_index"`
	EndIndex    int    `json:"end_index"`
	Type        string `json:"type"`
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

// Settings is the singleton user-settings row. Persisted in the DB (not env),
// since these are user-tunable.
type Settings struct {
	CEFRLevel                string    `json:"cefr_level"`
	TargetLanguage           string    `json:"target_language"`
	LLMModel                 string    `json:"llm_model"`
	MinDifficultyToHighlight string    `json:"min_difficulty_to_highlight"`
	UpdatedAt                time.Time `json:"updated_at"`
}

// SettingsPatch is a partial update of Settings for PATCH /api/settings. Nil
// fields are left unchanged; non-nil fields are applied. UpdatedAt is set by
// the store on apply.
type SettingsPatch struct {
	CEFRLevel                *string `json:"cefr_level,omitempty"`
	TargetLanguage           *string `json:"target_language,omitempty"`
	LLMModel                 *string `json:"llm_model,omitempty"`
	MinDifficultyToHighlight *string `json:"min_difficulty_to_highlight,omitempty"`
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
	ID                string    `json:"id"`
	Title             string    `json:"title"`
	Author            string    `json:"author"`
	SourceDomain      string    `json:"source_domain"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"created_at"`
	EnrichedAt        time.Time `json:"enriched_at,omitzero"`
	EnrichmentVersion int       `json:"enrichment_version"`
	TokenCount        int       `json:"token_count"`
}

// ArticlePayload is the full enriched-article response from
// GET /api/articles/:id. Enrichment may be nil if the article is not yet
// enriched (in which case the handler typically returns 409, but Status
// communicates the state either way).
type ArticlePayload struct {
	ID                string      `json:"id"`
	Status            string      `json:"status"`
	Title             string      `json:"title"`
	Author            string      `json:"author"`
	Lang              string      `json:"lang"`
	OriginalText      string      `json:"original_text"`
	Tokens            []Token     `json:"tokens"`
	Enrichment        *Enrichment `json:"enrichment,omitempty"`
	EnrichmentVersion int         `json:"enrichment_version"`
}

// ConfigResponse is the single bootstrap/delta-sync response from
// GET /api/config. ServerTime is the authoritative clock the client should use
// as the next sync cursor.
type ConfigResponse struct {
	Settings       Settings       `json:"settings"`
	Articles       []ArticleMeta  `json:"articles"`
	Progress       []Progress     `json:"progress"`
	MarkdownBudget MarkdownBudget `json:"markdown_budget"`
	ServerTime     time.Time      `json:"server_time"`
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

// AddArticleRequest is the POST /api/articles body.
type AddArticleRequest struct {
	URL string `json:"url"`
}

// AddArticleResponse is the POST /api/articles response. Deduplication is
// transparent: a duplicate URL returns the existing article's id/status.
type AddArticleResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
