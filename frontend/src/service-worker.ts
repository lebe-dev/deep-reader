/// <reference types="@sveltejs/kit" />
/// <reference lib="webworker" />

// SvelteKit service worker for Deep Reader (spec §6 shell, §10 offline).
// Uses SvelteKit's built-in $service-worker virtual module — no Workbox needed.
//
// Caching strategy:
//  - App shell (build + files + prerendered) → PRECACHE on install, CACHE-FIRST at runtime.
//  - GET /api/config → STALE-WHILE-REVALIDATE.
//  - GET /api/articles/:id → CACHE-FIRST (articles are immutable once enriched).
//  - SPA navigations (non-API) → serve cached index.html shell offline.
//  - Write methods (POST/PUT/PATCH/DELETE) → always pass through to network.

import { build, files, version } from '$service-worker';

declare let self: ServiceWorkerGlobalScope;

// ---------------------------------------------------------------------------
// Cache names — versioned so old entries are cleaned up on activate.
// ---------------------------------------------------------------------------

const SHELL_CACHE = `shell-${version}`;
const API_CACHE = `api-${version}`;

// All assets to precache: build artefacts + static files.
const SHELL_ASSETS = [...build, ...files];

// ---------------------------------------------------------------------------
// Install — precache the app shell.
// ---------------------------------------------------------------------------

self.addEventListener('install', (event: ExtendableEvent) => {
	// Note: we deliberately do NOT call skipWaiting() here. The new worker stays
	// in the "waiting" state so the UpdateBanner can offer an explicit "Обновить".
	// The page activates it on demand via the SKIP_WAITING message below.
	event.waitUntil(caches.open(SHELL_CACHE).then((cache) => cache.addAll(SHELL_ASSETS)));
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

	// --- GET /api/config → stale-while-revalidate ---
	if (path === '/api/config') {
		event.respondWith(staleWhileRevalidate(request, API_CACHE));
		return;
	}

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

/** Cache-first: return cached response; update cache from network in background. */
async function cacheFirst(request: Request, cacheName: string): Promise<Response> {
	const cache = await caches.open(cacheName);
	const cached = await cache.match(request);
	if (cached) return cached;

	const fresh = await fetch(request);
	if (fresh.ok) cache.put(request, fresh.clone());
	return fresh;
}

/** Stale-while-revalidate: return cache immediately; refresh in background. */
async function staleWhileRevalidate(request: Request, cacheName: string): Promise<Response> {
	const cache = await caches.open(cacheName);
	const cached = await cache.match(request);

	// Kick off a background refresh regardless of cache hit.
	const networkPromise = fetch(request)
		.then((fresh) => {
			if (fresh.ok) cache.put(request, fresh.clone());
			return fresh;
		})
		.catch(() => null);

	if (cached) return cached;

	// Nothing cached yet — wait for the network.
	const fresh = await networkPromise;
	if (fresh) return fresh;

	return new Response('Service Unavailable', { status: 503 });
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

	// Last resort — no shell cached yet.
	return new Response('Offline — please reload when connected.', {
		status: 503,
		headers: { 'Content-Type': 'text/plain' }
	});
}
