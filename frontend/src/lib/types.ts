// TypeScript mirror of the backend data model (deep-reader-architecture.md §8/§9).
//
// JSON field names match the backend wire format (snake_case). All names are
// exported individually (named exports) per project conventions.

// ---------------------------------------------------------------------------
// Enums / unions
// ---------------------------------------------------------------------------

/**
 * Processing status of an article — an explicit two-stage pipeline (fetch then
 * enrich) so the UI can show which stage it is in and which stage failed.
 *
 * - `queued`        — created, waiting to fetch original content
 * - `fetching`      — fetch/extract in flight
 * - `fetched`       — original content received, waiting for processing
 * - `enriching`     — sent for processing (LLM) in flight
 * - `enriched`      — ready (terminal success)
 * - `fetch_failed`  — fetch stage failed (retry re-fetches)
 * - `enrich_failed` — processing stage failed (retry re-enriches; content kept)
 */
export type Status =
	| 'queued'
	| 'fetching'
	| 'fetched'
	| 'enriching'
	| 'enriched'
	| 'fetch_failed'
	| 'enrich_failed';

/** CEFR proficiency levels (spec §8 `settings.cefr_level`). */
export type CefrLevel = 'A2' | 'B1' | 'B2' | 'C1' | 'C2';

/** Kind of a marked-up multi-token phrase (spec §8 `enrichments.phrases.type`). */
export type PhraseType = 'idiom' | 'phrasal_verb' | 'term';

// ---------------------------------------------------------------------------
// Tokens (deterministic backend tokenization, spec §5 "Tokenize")
// ---------------------------------------------------------------------------

/**
 * A single token produced by the backend tokenizer. The tokenizer emits only
 * *word* tokens (whitespace and punctuation are split boundaries, not tokens),
 * so the array index equals `index`. `start`/`end` are *byte* offsets into the
 * UTF-8 encoding of `Article.original_text` (Go semantics), such that the
 * encoded `original_text[start:end]` equals `text`. Contractions (`don't`) and
 * compounds (`well-known`) are a single token. The reader reconstructs the
 * whitespace/punctuation between tokens from `original_text` using these
 * offsets.
 */
export interface Token {
	/** Position of this token within the tokens array. */
	index: number;
	/** Exact substring from the original text. */
	text: string;
	/** Start byte offset (inclusive) in the UTF-8 encoded original text. */
	start: number;
	/** End byte offset (exclusive) in the UTF-8 encoded original text. */
	end: number;
}

// ---------------------------------------------------------------------------
// Enrichment payload pieces (spec §8 `enrichments`)
// ---------------------------------------------------------------------------

/** A word above the user's level, translated in context. */
export interface DifficultWord {
	token_index: number;
	lemma: string;
	translation: string;
	cefr_level: CefrLevel;
}

/** An idiom / phrasal verb / domain term spanning a token range. */
export interface Phrase {
	start_index: number;
	end_index: number;
	type: PhraseType;
	/**
	 * The literal phrase text spanned by [start_index, end_index], echoed by the
	 * backend. The enrichment pipeline validates it against the token range, so
	 * the range is guaranteed to match these words (no over-wide term tooltips).
	 */
	text: string;
	/** Contextual translation or definition (backend wire field: `translation`). */
	translation: string;
}

/** A full-sentence translation spanning a token range. */
export interface Sentence {
	start_index: number;
	end_index: number;
	translation: string;
}

/** A domain term worth defining rather than translating. */
export interface GlossaryItem {
	term: string;
	definition: string;
}

/** The structured LLM enrichment result for an article (spec §8). */
export interface Enrichment {
	difficult_words: DifficultWord[];
	phrases: Phrase[];
	sentences: Sentence[];
	glossary: GlossaryItem[];
}

// ---------------------------------------------------------------------------
// Articles (spec §8 `articles`)
// ---------------------------------------------------------------------------

/**
 * Library list metadata (`GET /api/config` returns an array of these).
 * Does not include the heavy `tokens` / `enrichment` payload.
 */
export interface ArticleMeta {
	id: string;
	source_url: string;
	title: string;
	author: string;
	source_domain: string;
	lang: string;
	status: Status;
	enrichment_version: number;
	/** Present when `status` is `fetch_failed` or `enrich_failed`. */
	error?: string;
	created_at: string;
	enriched_at?: string;
	updated_at: string;
	/** Number of tokens in the article; used to compute reading progress percentage. */
	token_count: number;
	/**
	 * Fraction [0,1] of tokens covered by at least one sentence translation —
	 * the enrichment-completeness signal. A value below 1 means the LLM left
	 * part of the article unannotated. Zero until enriched.
	 */
	enrichment_coverage: number;
}

/**
 * Full immutable article payload (`GET /api/articles/:id`): tokens plus the
 * enrichment structure. Cached aggressively client-side.
 */
export interface ArticlePayload {
	id: string;
	original_text: string;
	tokens: Token[];
	enrichment: Enrichment;
	enrichment_version: number;
	status: Status;
	/** Fraction [0,1] of tokens covered by sentence translations. See ArticleMeta. */
	enrichment_coverage: number;
}

/** Convenience type: full server-side article (meta + payload + original). */
export interface Article extends ArticleMeta {
	original_text: string;
	tokens: Token[];
	enrichment?: Enrichment;
}

// ---------------------------------------------------------------------------
// Progress (spec §8 `progress`)
// ---------------------------------------------------------------------------

/** Reading progress for an article, LWW-merged on `updated_at`. */
export interface Progress {
	article_id: string;
	/** Reading position (token index or percent, per backend convention). */
	position: number;
	is_read: boolean;
	updated_at: string;
}

// ---------------------------------------------------------------------------
// Settings (spec §8 `settings`)
// ---------------------------------------------------------------------------

/** User-editable settings singleton. */
export interface Settings {
	cefr_level: CefrLevel;
	/** Target translation language. MVP: hard `ru`, kept in schema. */
	target_language: string;
	llm_model: string;
	/** Minimum word level considered "difficult" (usually cefr_level + 1). */
	min_difficulty_to_highlight: CefrLevel;
	updated_at: string;
}

/** Partial settings patch body for `PATCH /api/settings`. */
export type SettingsPatch = Partial<
	Pick<Settings, 'cefr_level' | 'target_language' | 'llm_model' | 'min_difficulty_to_highlight'>
>;

// ---------------------------------------------------------------------------
// API response shapes (spec §9)
// ---------------------------------------------------------------------------

/**
 * Non-secret server deployment configuration returned on bootstrap.
 * Secrets (AUTH_TOKEN, LLM_API_KEY) are intentionally omitted.
 */
export interface ServerInfo {
	http_port: number;
	database_path: string;
	llm_api_base_url: string;
	llm_model: string;
	llm_max_concurrent: number;
	llm_request_timeout: string;
	llm_max_retries: number;
	readability_timeout: string;
	enrichment_version: number;
	markdown_enabled: boolean;
	markdown_base_url: string;
	markdown_timeout: string;
	markdown_daily_limit: number;
	markdown_cost_per_article: number;
	log_level: string;
	log_format: string;
}

/**
 * markdown.new daily request-unit budget (spec §11 "Стоимость и rate limiting").
 * Surfaced so the UI can show remaining capacity and warn before the free-plan
 * limit is hit. When markdown.new is disabled, `enabled` is false.
 */
export interface MarkdownBudget {
	enabled: boolean;
	/** Request-unit budget per UTC day (0 = unlimited). */
	daily_limit: number;
	/** Request units one article conversion costs. */
	cost_per_article: number;
	/** Request units consumed so far today. */
	units_used: number;
	/** Units left today (daily_limit - units_used, clamped at 0). */
	units_remaining: number;
	/** How many more conversions today's budget allows. */
	articles_remaining: number;
}

/** `GET /api/config` bootstrap/delta-sync response. */
export interface ConfigResponse {
	settings: Settings;
	articles: ArticleMeta[];
	progress: Progress[];
	/** markdown.new daily request-unit budget. */
	markdown_budget: MarkdownBudget;
	/** Non-secret server deployment configuration. */
	server_info: ServerInfo;
	/** Server cursor to pass back as `?since=` on the next delta sync. */
	cursor?: string;
}

/** `POST /api/articles` and `POST /api/articles/:id/retry` response. */
export interface AddArticleResponse {
	id: string;
	status: Status;
}

/** `PUT /api/articles/:id/progress` request body. */
export interface ProgressUpdate {
	position: number;
	is_read: boolean;
	updated_at: string;
}
