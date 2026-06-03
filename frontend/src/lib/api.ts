// HTTP client for the Deep Reader backend (deep-reader-architecture.md §9).
//
// - Base URL: same origin when the PWA is served by the backend; otherwise the
//   `serverUrl` configured in the sync-state singleton.
// - Auth: shared bearer token from sync-state, sent as `Authorization: Bearer`.
// - When the network is absent, requests throw a typed `OfflineError` so callers
//   can fall back to IndexedDB / the outbox (spec §10).

import { browser } from '$app/environment';
import { getSyncState } from './db';
import type {
	AddArticleResponse,
	ArticlePayload,
	AuthResponse,
	ConfigResponse,
	Progress,
	ProgressUpdate,
	ReEnrichMode,
	Settings,
	SettingsPatch
} from './types';

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

/** Thrown when a request cannot reach the network (offline / fetch failed). */
export class OfflineError extends Error {
	constructor(message = 'Network unavailable') {
		super(message);
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
type FetchCache =
	| 'default'
	| 'no-store'
	| 'reload'
	| 'no-cache'
	| 'force-cache'
	| 'only-if-cached';

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

	let res: Response;
	try {
		res = await fetch(target, {
			method: opts.method ?? 'GET',
			headers,
			body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
			signal: opts.signal,
			cache: opts.cache
		});
	} catch {
		// fetch rejects on network failure / offline.
		throw new OfflineError();
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

// ---------------------------------------------------------------------------
// Write endpoints
// ---------------------------------------------------------------------------

/** `POST /api/articles` — add an article by URL (dedup is transparent). */
export function addArticle(url: string, signal?: AbortSignal): Promise<AddArticleResponse> {
	return request<AddArticleResponse>('/api/articles', { method: 'POST', body: { url }, signal });
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
