// HTTP client for the Deep Reader backend (deep-reader-architecture.md §9).
//
// - Base URL: same origin when the PWA is served by the backend; otherwise the
//   `serverUrl` configured in the sync-state singleton.
// - Auth: shared bearer token from sync-state, sent as `Authorization: Bearer`.
// - When the network is absent, requests throw a typed `OfflineError` so callers
//   can fall back to IndexedDB / the outbox (spec §10).

import { browser } from '$app/environment';
import { getSyncState } from './db';
import { captureError } from './sentry';
import { httpRequest, type HttpResponse } from './platform/http';
import { isNative } from './platform';
import type {
	AddArticleResponse,
	ArticlePayload,
	ArticleRaw,
	AuthResponse,
	ConfigResponse,
	Progress,
	ProgressUpdate,
	ReEnrichMode,
	Settings,
	SettingsPatch,
	LLMProviderView,
	LLMProviderInput
} from './types';

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

/**
 * Thrown when a request cannot reach the network (offline / fetch failed).
 *
 * The original fetch rejection (a `TypeError` for DNS/TLS/CORS/mixed-content, or
 * any other connectivity failure) is preserved as `cause` so it is not lost when
 * a genuine connectivity bug masquerades as "offline".
 */
export class OfflineError extends Error {
	constructor(message = 'Network unavailable', options?: { cause?: unknown }) {
		super(message, options);
		this.name = 'OfflineError';
	}
}

/** Thrown for non-2xx HTTP responses. */
export class ApiError extends Error {
	readonly status: number;
	readonly body: string;
	constructor(status: number, body: string) {
		super(`API error ${status}`);
		this.name = 'ApiError';
		this.status = status;
		this.body = body;
	}
}

// ---------------------------------------------------------------------------
// Request plumbing
// ---------------------------------------------------------------------------

/** Resolve the API base URL. Same origin in the browser, else configured serverUrl. */
async function resolveBaseUrl(): Promise<string> {
	if (browser) {
		const state = await getSyncState();
		// When served by the backend the PWA lives on the same origin, so a
		// relative path is correct. A configured serverUrl overrides (e.g. dev).
		return state.serverUrl?.replace(/\/$/, '') ?? '';
	}
	return '';
}

/** Fetch cache modes (avoids relying on the DOM lib's RequestCache global). */
type FetchCache = 'default' | 'no-store' | 'reload' | 'no-cache' | 'force-cache' | 'only-if-cached';

/**
 * Whether a fetch rejection is a caller-initiated abort (an `AbortSignal` fired,
 * e.g. on navigation or a superseding request). Browsers throw a `DOMException`
 * with `name === 'AbortError'`; we match on the name so a stubbed/polyfilled
 * error works too. Aborts are not connectivity failures and must propagate
 * unchanged rather than being misread as "offline".
 */
function isAbortError(err: unknown): boolean {
	return (
		typeof err === 'object' && err !== null && (err as { name?: unknown }).name === 'AbortError'
	);
}

interface RequestOptions {
	method?: string;
	body?: unknown;
	/** Extra query params (omitted when undefined). */
	query?: Record<string, string | undefined>;
	signal?: AbortSignal;
	/** Overrides the fetch cache mode (e.g. 'no-store' to bypass the HTTP cache). */
	cache?: FetchCache;
}

async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
	const base = await resolveBaseUrl();

	// On native there is no same-origin fallback: an empty base would target
	// `capacitor://localhost`. Until the user configures a serverUrl, treat every
	// request as offline so refreshAuth degrades gracefully and the root layout
	// routes to /connect (MOBILE-ARCH.md §7) instead of firing doomed requests.
	if (isNative() && base === '') throw new OfflineError('No server configured');

	const state = await getSyncState();

	const url = new URL(`${base}${path}`, browser ? window.location.origin : 'http://localhost');
	if (opts.query) {
		for (const [key, value] of Object.entries(opts.query)) {
			if (value !== undefined) url.searchParams.set(key, value);
		}
	}

	const headers: Record<string, string> = { Accept: 'application/json' };
	if (state.authToken) headers.Authorization = `Bearer ${state.authToken}`;
	if (opts.body !== undefined) headers['Content-Type'] = 'application/json';

	// A relative base must keep the request relative so it stays same-origin.
	const target = base === '' ? `${path}${url.search}` : url.toString();

	let res: HttpResponse;
	try {
		res = await httpRequest(target, {
			method: opts.method ?? 'GET',
			headers,
			body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
			signal: opts.signal,
			cache: opts.cache
		});
	} catch (err) {
		// A caller-initiated abort is not a connectivity failure — propagate it
		// unchanged so callers can distinguish cancellation from "offline".
		if (isAbortError(err)) throw err;

		// Any other rejection (DNS/TLS/CORS/mixed-content/genuine offline) is a
		// network failure. If the browser still reports itself online the cause is
		// a real connectivity bug, not the user being offline — surface it to
		// Sentry so it does not silently hide as "offline". When truly offline we
		// expect this and stay quiet.
		if (typeof navigator !== 'undefined' && navigator.onLine) {
			captureError(err, { area: 'api', extra: { path } });
		}

		// Preserve the original rejection as the cause so the connectivity detail
		// survives even though callers branch on `instanceof OfflineError`.
		throw new OfflineError(undefined, { cause: err });
	}

	if (!res.ok) {
		const text = await res.text().catch(() => '');
		throw new ApiError(res.status, text);
	}

	if (res.status === 204) return undefined as T;
	const text = await res.text();
	if (!text) return undefined as T;
	return JSON.parse(text) as T;
}

// ---------------------------------------------------------------------------
// Read endpoints
// ---------------------------------------------------------------------------

/** `GET /api/config` — bootstrap / delta sync. Pass `since` for a delta cursor. */
export function getConfig(since?: string, signal?: AbortSignal): Promise<ConfigResponse> {
	return request<ConfigResponse>('/api/config', { query: { since }, signal });
}

// ---------------------------------------------------------------------------
// Auth endpoints
// ---------------------------------------------------------------------------

/** `POST /api/setup` — first-run creation of the single built-in account. */
export function setup(
	username: string,
	password: string,
	signal?: AbortSignal
): Promise<AuthResponse> {
	return request<AuthResponse>('/api/setup', {
		method: 'POST',
		body: { username, password },
		signal
	});
}

/** `POST /api/login` — verify credentials and obtain a session token. */
export function login(
	username: string,
	password: string,
	signal?: AbortSignal
): Promise<AuthResponse> {
	return request<AuthResponse>('/api/login', {
		method: 'POST',
		body: { username, password },
		signal
	});
}

/** `POST /api/logout` — end the current session server-side. */
export function logout(signal?: AbortSignal): Promise<void> {
	return request<void>('/api/logout', { method: 'POST', signal });
}

/**
 * `GET /api/articles/:id` — full article payload.
 *
 * Enriched payloads are served with a long immutable Cache-Control. A re-enrich
 * changes the content but keeps the same enrichment_version, so callers pass the
 * article's `version` (its updated_at) as a cache-busting query param: each
 * distinct version is a fresh URL the browser caches immutably, and a re-enrich
 * (which bumps updated_at) is fetched anew instead of served stale. When polling
 * mid-re-enrich, pass `{ noStore: true }` to bypass the HTTP cache entirely.
 */
export function getArticle(
	id: string,
	signal?: AbortSignal,
	opts?: { noStore?: boolean; version?: string }
): Promise<ArticlePayload> {
	return request<ArticlePayload>(`/api/articles/${encodeURIComponent(id)}`, {
		signal,
		query: { v: opts?.version },
		cache: opts?.noStore ? 'no-store' : undefined
	});
}

/**
 * `GET /api/articles/:id/raw` — the raw LLM response captured when enrichment
 * failed to decode the provider's answer. A debugging aid for `enrich_failed`
 * articles; `raw` is empty when nothing was captured.
 */
export function getArticleRaw(id: string, signal?: AbortSignal): Promise<ArticleRaw> {
	return request<ArticleRaw>(`/api/articles/${encodeURIComponent(id)}/raw`, {
		signal,
		cache: 'no-store'
	});
}

// ---------------------------------------------------------------------------
// Write endpoints
// ---------------------------------------------------------------------------

/** `POST /api/articles` — add an article by URL (dedup is transparent). */
export function addArticle(url: string, signal?: AbortSignal): Promise<AddArticleResponse> {
	return request<AddArticleResponse>('/api/articles', { method: 'POST', body: { url }, signal });
}

/**
 * `POST /api/articles` — add an article from pasted raw text. Both the title
 * and the source URL (a link back to the original article, stored as metadata)
 * are optional.
 */
export function addArticleText(
	text: string,
	title?: string,
	sourceUrl?: string,
	signal?: AbortSignal
): Promise<AddArticleResponse> {
	return request<AddArticleResponse>('/api/articles', {
		method: 'POST',
		body: { text, title: title ?? '', url: sourceUrl ?? '' },
		signal
	});
}

/** `DELETE /api/articles/:id` — remove an article from the library. */
export function deleteArticle(id: string, signal?: AbortSignal): Promise<void> {
	return request<void>(`/api/articles/${encodeURIComponent(id)}`, { method: 'DELETE', signal });
}

/** `POST /api/articles/:id/retry` — resume a failed article from the stage that failed. */
export function retryArticle(id: string, signal?: AbortSignal): Promise<AddArticleResponse> {
	return request<AddArticleResponse>(`/api/articles/${encodeURIComponent(id)}/retry`, {
		method: 'POST',
		signal
	});
}

/**
 * `POST /api/articles/:id/reenrich` — re-run enrichment for an enriched article.
 * `mode` "full" re-translates the whole article; "topup" fills only the spans no
 * sentence covers yet.
 */
export function reEnrichArticle(
	id: string,
	mode: ReEnrichMode,
	signal?: AbortSignal
): Promise<AddArticleResponse> {
	return request<AddArticleResponse>(`/api/articles/${encodeURIComponent(id)}/reenrich`, {
		method: 'POST',
		body: { mode },
		signal
	});
}

/** `PUT /api/articles/:id/progress` — update reading position / read flag (LWW). */
export function putProgress(p: Progress, signal?: AbortSignal): Promise<void> {
	const body: ProgressUpdate = {
		position: p.position,
		is_read: p.is_read,
		updated_at: p.updated_at
	};
	return request<void>(`/api/articles/${encodeURIComponent(p.article_id)}/progress`, {
		method: 'PUT',
		body,
		signal
	});
}

/** `PUT /api/articles/:id/pin` — toggle the library pin flag. */
export function pinArticle(id: string, pinned: boolean, signal?: AbortSignal): Promise<void> {
	return request<void>(`/api/articles/${encodeURIComponent(id)}/pin`, {
		method: 'PUT',
		body: { pinned },
		signal
	});
}

/** `PATCH /api/settings` — update user settings (partial). */
export function patchSettings(partial: SettingsPatch, signal?: AbortSignal): Promise<Settings> {
	return request<Settings>('/api/settings', { method: 'PATCH', body: partial, signal });
}

// ---------------------------------------------------------------------------
// LLM provider profiles (backend-only; never goes through the offline outbox).
// ---------------------------------------------------------------------------

/** `GET /api/llm-providers` — list connection profiles (API keys masked). */
export async function listLLMProviders(signal?: AbortSignal): Promise<LLMProviderView[]> {
	const res = await request<{ providers: LLMProviderView[] }>('/api/llm-providers', { signal });
	return res?.providers ?? [];
}

/** `POST /api/llm-providers` — create a profile. The first one becomes active. */
export function createLLMProvider(
	input: LLMProviderInput,
	signal?: AbortSignal
): Promise<LLMProviderView> {
	return request<LLMProviderView>('/api/llm-providers', { method: 'POST', body: input, signal });
}

/** `PATCH /api/llm-providers/:id` — update a profile (omit api_key to keep it). */
export function updateLLMProvider(
	id: string,
	input: LLMProviderInput,
	signal?: AbortSignal
): Promise<LLMProviderView> {
	return request<LLMProviderView>(`/api/llm-providers/${encodeURIComponent(id)}`, {
		method: 'PATCH',
		body: input,
		signal
	});
}

/** `DELETE /api/llm-providers/:id` — remove a profile. */
export function deleteLLMProvider(id: string, signal?: AbortSignal): Promise<void> {
	return request<void>(`/api/llm-providers/${encodeURIComponent(id)}`, {
		method: 'DELETE',
		signal
	});
}

/** `POST /api/llm-providers/:id/activate` — make a profile the active connection. */
export function activateLLMProvider(id: string, signal?: AbortSignal): Promise<void> {
	return request<void>(`/api/llm-providers/${encodeURIComponent(id)}/activate`, {
		method: 'POST',
		signal
	});
}
