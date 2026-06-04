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
 * - `blocked`       — fetched a bot-verification / captcha page, not the article
 *                     (detected before any LLM call; retry re-fetches)
 */
export type Status =
	| 'queued'
	| 'fetching'
	| 'fetched'
	| 'enriching'
	| 'enriched'
	| 'fetch_failed'
	| 'enrich_failed'
	| 'blocked'
	| 'topup_queued';

/**
 * Re-enrichment mode for the article-page "improve translation" tools:
 * `full` re-translates the whole article, `topup` fills only the spans no
 * sentence covers yet (model.ReEnrichMode*).
 */
export type ReEnrichMode = 'full' | 'topup';

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
	/** User flag keeping the article at the top of the library. Synced as metadata. */
	pinned: boolean;
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
	/** Short LLM-produced abstract; empty until the article has been summarized. */
	summary?: string;
}

/**
 * Full immutable article payload (`GET /api/articles/:id`): tokens plus the
 * enrichment structure. Cached aggressively client-side.
 */
export interface ArticlePayload {
	id: string;
	original_text: string;
	tokens: Token[];
	/** Short LLM-produced abstract shown in the reader. Empty until summarized. */
	summary?: string;
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
	/** Show the low-budget banner when ≤ N markdown.new conversions remain (0 = off). */
	markdown_warn_threshold: number;
	/** Custom enrichment system-prompt template. Empty = use the server default. */
	enrichment_prompt: string;
	/** Custom summary system-prompt template. Empty = use the server default. */
	summary_prompt: string;
	/** Custom bot-wall / captcha signatures (one per line). Empty = use the server defaults. */
	bot_wall_signatures: string;
	/** Step-wise enrichment window size (tokens per chunk). 0 = use the server default. */
	chunk_tokens: number;
	updated_at: string;
}

/** Partial settings patch body for `PATCH /api/settings`. */
export type SettingsPatch = Partial<
	Pick<
		Settings,
		| 'cefr_level'
		| 'target_language'
		| 'llm_model'
		| 'min_difficulty_to_highlight'
		| 'markdown_warn_threshold'
		| 'enrichment_prompt'
		| 'summary_prompt'
		| 'bot_wall_signatures'
		| 'chunk_tokens'
	>
>;

// ---------------------------------------------------------------------------
// API response shapes (spec §9)
// ---------------------------------------------------------------------------

/**
 * Non-secret server deployment configuration returned on bootstrap.
 * Secrets (LLM_API_KEY, password hashes) are intentionally omitted.
 */
export interface ServerInfo {
	http_port: number;
	database_path: string;
	trust_proxy: boolean;
	trusted_proxies: string[];
	login_max_attempts: number;
	login_attempt_window: string;
	login_lockout_duration: string;
	llm_api_base_url: string;
	llm_model: string;
	llm_max_concurrent: number;
	llm_request_timeout: string;
	llm_max_retries: number;
	/** Default step-wise enrichment window size (tokens per chunk). */
	llm_chunk_tokens: number;
	readability_timeout: string;
	enrichment_version: number;
	/** Built-in enrichment prompt template the client pre-fills / resets to. */
	enrichment_prompt_default: string;
	/** Built-in summary prompt template the client pre-fills / resets to. */
	summary_prompt_default: string;
	/** Built-in bot-wall / captcha signature list the client pre-fills / resets to. */
	bot_wall_signatures_default: string;
	markdown_enabled: boolean;
	markdown_base_url: string;
	markdown_timeout: string;
	markdown_daily_limit: number;
	markdown_cost_per_article: number;
	log_level: string;
	log_format: string;
	/** Masked backend Sentry DSN (asterisks); empty when unset. */
	sentry_dsn: string;
	/** Masked browser Sentry DSN (asterisks); empty when unset. */
	sentry_frontend_dsn: string;
	/** Sentry environment tag (verbatim); empty when unset. */
	sentry_environment: string;
	version: string;
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

/**
 * Authentication state carried by `GET /api/config` — the only endpoint
 * reachable before login. The client routes on it: `/setup` when not
 * initialized, `/login` when initialized but unauthenticated, into the app when
 * authenticated.
 */
export interface AuthStatus {
	/** True once the single built-in account has been created. */
	initialized: boolean;
	/** True when the request carried a valid session token. */
	authenticated: boolean;
}

/** Response of `POST /api/setup` and `POST /api/login`. */
export interface AuthResponse {
	/** Opaque session bearer token to store and send as `Authorization: Bearer`. */
	token: string;
	username: string;
}

/**
 * `GET /api/config` bootstrap/delta-sync response.
 *
 * When the caller is unauthenticated only `auth` and `server_time` are
 * populated; the library fields are present only on an authenticated response.
 */
export interface ConfigResponse {
	auth: AuthStatus;
	settings: Settings;
	articles: ArticleMeta[];
	progress: Progress[];
	/** markdown.new daily request-unit budget. */
	markdown_budget: MarkdownBudget;
	/** Non-secret server deployment configuration. */
	server_info: ServerInfo;
	/** Browser Sentry config; `dsn` empty when frontend reporting is disabled. */
	sentry: SentryConfig;
	/** Server cursor to pass back as `?since=` on the next delta sync. */
	cursor?: string;
}

/**
 * Browser Sentry configuration delivered on bootstrap. The DSN is public by
 * design (browser SDKs embed it), so this carries no secret. An empty `dsn`
 * means frontend error reporting is disabled.
 */
export interface SentryConfig {
	dsn: string;
	environment: string;
	release: string;
}

/**
 * `GET /api/articles/:id/raw` response — the verbatim LLM output captured when
 * the enrichment stage failed to decode the provider's answer. `raw` is empty
 * when nothing was captured (e.g. a fetch failure or a network/HTTP error).
 */
export interface ArticleRaw {
	id: string;
	status: Status;
	/** Stored failure message (same as `ArticleMeta.error`). */
	error?: string;
	/** Verbatim model output that failed to decode. */
	raw: string;
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
