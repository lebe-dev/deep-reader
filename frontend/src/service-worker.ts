/// <reference types="@sveltejs/kit" />
/// <reference lib="webworker" />

// SvelteKit service worker for Deep Reader (spec §6 shell, §10 offline).
// Uses SvelteKit's built-in $service-worker virtual module — no Workbox needed.
//
// Caching strategy:
//  - App shell (build + files + prerendered) → PRECACHE on install, CACHE-FIRST at runtime.
//  - GET /api/config → NETWORK-ONLY (carries the auth flag; must never be stale).
//  - GET /api/articles/:id → CACHE-FIRST (articles are immutable once enriched),
//    UNLESS the request opts out of the cache (cache:'no-store'/'reload'), e.g.
//    the reader's re-enrich poll — those go NETWORK-FIRST so the poll observes the
//    freshly re-enriched payload instead of a stale cached copy.
//  - SPA navigations (non-API) → serve cached index.html shell offline.
//  - Write methods (POST/PUT/PATCH/DELETE) → always pass through to network.

import { build, files, version } from '$service-worker';
import { precacheAll } from '$lib/pwa/precache';

declare let self: ServiceWorkerGlobalScope;

// ---------------------------------------------------------------------------
// Cache names — versioned so old entries are cleaned up on activate.
// ---------------------------------------------------------------------------

const SHELL_CACHE = `shell-${version}`;
const API_CACHE = `api-${version}`;

// All assets to precache: build artefacts + static files.
const SHELL_ASSETS = [...build, ...files];

// ---------------------------------------------------------------------------
// Error reporting — the SW runs in a separate scope the page's Sentry SDK does
// not cover. We have no DSN here (it arrives via /api/config, which the SW never
// fetches), so we broadcast structured failures to every controlled client and
// let the page forward them to Sentry with the `area:'sw'` tag. We also keep a
// console.* at each call site so failures are never fully silent even when no
// page is listening.
// ---------------------------------------------------------------------------

/** Shape posted to clients for an SW-scope failure (consumed by the page SDK). */
interface SwErrorMessage {
	type: 'SW_ERROR';
	area: 'sw';
	/** Where in the SW it failed, so the page can tag/group it. */
	stage: 'precache' | 'fetch' | 'shell-fallback';
	/** Human-readable summary (never carries request bodies / credentials). */
	message: string;
	/** Optional structured detail (url, status, failed-asset count…). */
	detail?: Record<string, unknown>;
}

/** Broadcast an SW failure to all controlled clients for forwarding to Sentry. */
async function reportSwError(msg: Omit<SwErrorMessage, 'type' | 'area'>): Promise<void> {
	try {
		const clients = await self.clients.matchAll({ includeUncontrolled: true });
		const payload: SwErrorMessage = { type: 'SW_ERROR', area: 'sw', ...msg };
		for (const client of clients) client.postMessage(payload);
	} catch {
		// Reporting must never throw into the fetch/install handler.
	}
}

// ---------------------------------------------------------------------------
// Install — precache the app shell.
// ---------------------------------------------------------------------------

self.addEventListener('install', (event: ExtendableEvent) => {
	// Note: we deliberately do NOT call skipWaiting() here. The new worker stays
	// in the "waiting" state so the UpdateBanner can offer an explicit "Обновить".
	// The page activates it on demand via the SKIP_WAITING message below.
	//
	// We precache resiliently (per-asset) rather than with the atomic Cache.addAll:
	// a single unfetchable asset must not abort the whole install and brick the PWA
	// (see precacheAll). Failures are logged AND reported, not swallowed.
	event.waitUntil(
		caches.open(SHELL_CACHE).then(async (cache) => {
			const failed = await precacheAll(cache, SHELL_ASSETS);
			if (failed.length === 0) return;

			console.warn(
				`[sw] precache: ${failed.length}/${SHELL_ASSETS.length} assets failed to cache`,
				failed
			);
			await reportSwError({
				stage: 'precache',
				message: `precache: ${failed.length}/${SHELL_ASSETS.length} assets failed`,
				detail: { failedCount: failed.length, total: SHELL_ASSETS.length, failed }
			});
		})
	);
});

// ---------------------------------------------------------------------------
// Message — let the page activate a waiting worker on demand.
// ---------------------------------------------------------------------------

self.addEventListener('message', (event: ExtendableMessageEvent) => {
	if (event.data?.type === 'SKIP_WAITING') {
		self.skipWaiting();
	}
});

// ---------------------------------------------------------------------------
// Activate — clean up old versioned caches.
// ---------------------------------------------------------------------------

self.addEventListener('activate', (event: ExtendableEvent) => {
	event.waitUntil(
		caches
			.keys()
			.then((keys) => {
				const obsolete = keys.filter((k) => k !== SHELL_CACHE && k !== API_CACHE);
				return Promise.all(obsolete.map((k) => caches.delete(k)));
			})
			.then(() => self.clients.claim())
	);
});

// ---------------------------------------------------------------------------
// Fetch — route requests to the appropriate strategy.
// ---------------------------------------------------------------------------

self.addEventListener('fetch', (event: FetchEvent) => {
	const { request } = event;
	const url = new URL(request.url);

	// Never intercept non-GET write methods — let them hit the network.
	if (request.method !== 'GET') return;

	// Never intercept cross-origin requests.
	if (url.origin !== self.location.origin) return;

	const path = url.pathname;

	// --- GET /api/config → network-only ---
	// It carries the auth/setup flag, so a stale cached copy could keep a
	// logged-out client looking authenticated. Let it hit the network directly.
	if (path === '/api/config') return;

	// --- GET /api/articles/:id → cache-first (immutable after enrichment) ---
	if (/^\/api\/articles\/[^/]+$/.test(path)) {
		event.respondWith(cacheFirst(request, API_CACHE));
		return;
	}

	// --- Other /api/* paths → network only (never cache writes or list endpoints) ---
	if (path.startsWith('/api/')) return;

	// --- App shell / SPA navigation → cache-first, fall back to index.html ---
	event.respondWith(shellOrIndex(request));
});

// ---------------------------------------------------------------------------
// Strategy helpers
// ---------------------------------------------------------------------------

/**
 * Whether the request explicitly opts out of the HTTP cache. The reader's
 * re-enrich poll fetches with `cache:'no-store'` to observe the freshly
 * re-enriched payload; honouring that here is what stops the poll from being
 * served a stale cached copy forever (and timing out).
 */
function bypassesCache(request: Request): boolean {
	return request.cache === 'no-store' || request.cache === 'reload';
}

/**
 * Cache-first for immutable article payloads, network-first when the caller opts
 * out of the cache (cache:'no-store'/'reload').
 *
 * Cache-first path: return the cached response if present; otherwise fetch from
 * the network and populate the cache. This is NOT stale-while-revalidate — a hit
 * is served as-is with no background refresh, which is correct because each
 * distinct payload version is requested under its own `?v=<updated_at>` URL
 * (see api.getArticle), so a re-enriched article is a fresh cache key, never a
 * stale hit.
 *
 * Network-first path (bypass): fetch fresh, update the cache on success, and only
 * fall back to any cached copy if the network fails. This is what the re-enrich
 * poll relies on to see the new translation.
 */
async function cacheFirst(request: Request, cacheName: string): Promise<Response> {
	const cache = await caches.open(cacheName);

	if (bypassesCache(request)) return networkFirst(request, cache);

	const cached = await cache.match(request);
	if (cached) return cached;

	try {
		const fresh = await fetch(request);
		if (fresh.ok) cache.put(request, fresh.clone());
		return fresh;
	} catch (err) {
		await reportSwError({
			stage: 'fetch',
			message: `cache-first network fetch failed: ${url(request)}`,
			detail: { url: url(request) }
		});
		throw err;
	}
}

/**
 * Network-first: always try the network so a fresh payload (e.g. a re-enriched
 * article during polling) is observed; update the cache on success and fall back
 * to a cached copy only when the network is unreachable.
 */
async function networkFirst(request: Request, cache: Cache): Promise<Response> {
	try {
		const fresh = await fetch(request);
		if (fresh.ok) cache.put(request, fresh.clone());
		return fresh;
	} catch (err) {
		const cached = await cache.match(request);
		if (cached) return cached;

		await reportSwError({
			stage: 'fetch',
			message: `network-first fetch failed with no cached fallback: ${url(request)}`,
			detail: { url: url(request) }
		});
		throw err;
	}
}

/**
 * Shell or index: serve the exact static asset from the shell cache;
 * for unknown paths (SPA routes) fall back to the cached index.html so
 * the app renders offline.
 */
async function shellOrIndex(request: Request): Promise<Response> {
	const cache = await caches.open(SHELL_CACHE);
	const cached = await cache.match(request);
	if (cached) return cached;

	// Try the network for the exact resource first.
	try {
		const fresh = await fetch(request);
		if (fresh.ok) {
			cache.put(request, fresh.clone());
			return fresh;
		}
	} catch {
		// Offline — fall through to index.html.
	}

	// SPA fallback: serve the cached app shell root.
	const shell = await cache.match('/');
	if (shell) return shell;

	// Last resort — no shell cached yet. This means the install-time precache
	// never populated the shell (e.g. it failed) and we are offline, so the user
	// sees a bare 503. Report it so the otherwise-silent failure is visible.
	await reportSwError({
		stage: 'shell-fallback',
		message: 'no shell cached: serving 503 offline last-resort',
		detail: { url: url(request) }
	});
	return new Response('Offline — please reload when connected.', {
		status: 503,
		headers: { 'Content-Type': 'text/plain' }
	});
}

/** Best-effort request URL for diagnostics (never throws). */
function url(request: Request): string {
	try {
		return new URL(request.url).pathname;
	} catch {
		return request.url;
	}
}
