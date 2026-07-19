// HTTP transport abstraction (MOBILE-ARCH.md §6.2).
//
// `request()` in api.ts calls `httpRequest()` instead of `fetch()` directly so
// the same request plumbing (status handling, OfflineError/ApiError, Sentry)
// works on both platforms:
//
//   - web    → native `fetch` (behaviour is bit-for-bit the current one).
//   - native → `CapacitorHttp` from @capacitor/core, which issues the request
//              from the native layer and thereby bypasses CORS (D2): the backend
//              keeps its closed CORS policy and web users are unaffected.
//
// The returned object is `fetch`-compatible in the subset api.ts uses (ok,
// status, text()), so callers need no branching. Network failures are surfaced
// as ordinary rejections that api.ts's single catch turns into `OfflineError`;
// aborts are surfaced as an `AbortError` so they propagate unchanged.

import { isNative } from './index';

/** The subset of `Response` that api.ts relies on. */
export type HttpResponse = {
	ok: boolean;
	status: number;
	json(): Promise<unknown>;
	text(): Promise<string>;
};

/** Fetch cache modes (mirrors api.ts's `FetchCache`; the DOM lib type is not a global). */
type FetchCache = 'default' | 'no-store' | 'reload' | 'no-cache' | 'force-cache' | 'only-if-cached';

/**
 * A `fetch`-style request options object, narrowed to what api.ts actually
 * sends. Declared locally rather than reusing the DOM `RequestInit` type, which
 * eslint's `no-undef` flags because it is a type-only global (same reason api.ts
 * defines its own `FetchCache`). It stays assignable to `fetch`'s `RequestInit`.
 */
export interface HttpRequestInit {
	method?: string;
	headers?: Record<string, string>;
	/** Already JSON-serialised by api.ts, with a matching Content-Type header. */
	body?: string;
	signal?: AbortSignal;
	/** SW-cache bypass on web; ignored on native (§6.2). */
	cache?: FetchCache;
}

/**
 * Issue an HTTP request. On web this is `fetch`; on native it is CapacitorHttp.
 * The `cache` option has no effect on native and is ignored there.
 */
export async function httpRequest(url: string, init: HttpRequestInit = {}): Promise<HttpResponse> {
	if (!isNative()) return fetch(url, init);
	return nativeRequest(url, init);
}

// ---------------------------------------------------------------------------
// Native (CapacitorHttp) transport
// ---------------------------------------------------------------------------

async function nativeRequest(url: string, init: HttpRequestInit): Promise<HttpResponse> {
	// A caller-initiated abort must look exactly like fetch's: reject with an
	// `AbortError` so api.ts's `isAbortError` check propagates it unchanged.
	if (init.signal?.aborted) throw abortError();

	const { CapacitorHttp } = await import('@capacitor/core');

	// api.ts hands us a JSON string body with `Content-Type: application/json`.
	// CapacitorHttp serialises `data` itself according to that header, so parse
	// the string back into a value and let the plugin re-encode it — passing the
	// raw string would be double-encoded.
	const data = init.body != null ? JSON.parse(init.body) : undefined;

	const pending = CapacitorHttp.request({
		url,
		method: (init.method ?? 'GET').toUpperCase(),
		headers: init.headers ?? {},
		data,
		// CapacitorHttp does not throw on 4xx/5xx — it returns the status, matching
		// fetch semantics that api.ts depends on. Ask it not to decode the body so
		// we control (de)serialisation and mirror `Response.text()` exactly.
		responseType: 'text'
	}).then(mapResponse);

	// CapacitorHttp cannot cancel an in-flight request, but the caller only needs
	// to observe the abort (navigation, superseding request). Race the response
	// against the signal so a later abort still rejects with an AbortError.
	if (!init.signal) return pending;
	return Promise.race([pending, abortWhenSignalled(init.signal)]);
}

/** Map a CapacitorHttp result into the `fetch`-compatible `HttpResponse`. */
function mapResponse(res: { status: number; data: unknown }): HttpResponse {
	const status = res.status;
	// With responseType 'text' the body is a string; guard the object case (some
	// plugin versions still auto-parse JSON) by re-serialising so text() is stable.
	const bodyText = typeof res.data === 'string' ? res.data : JSON.stringify(res.data ?? '');
	return {
		ok: status >= 200 && status < 300,
		status,
		text: () => Promise.resolve(bodyText),
		json: () => Promise.resolve(bodyText ? JSON.parse(bodyText) : undefined)
	};
}

/** A DOMException that matches fetch's abort rejection (`name === 'AbortError'`). */
function abortError(): DOMException {
	return new DOMException('The operation was aborted.', 'AbortError');
}

/** Reject with an `AbortError` the moment `signal` fires. Never resolves. */
function abortWhenSignalled(signal: AbortSignal): Promise<never> {
	return new Promise((_, reject) => {
		// The signal may already have fired during the `await import` above, before
		// this listener is attached — that event is missed, so check upfront.
		if (signal.aborted) {
			reject(abortError());
			return;
		}
		signal.addEventListener('abort', () => reject(abortError()), { once: true });
	});
}
