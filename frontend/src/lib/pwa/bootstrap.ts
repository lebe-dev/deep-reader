// PWA bootstrap — registers the service worker and requests persistent storage.
//
// INVOCATION CONTRACT
// -------------------
// This module CANNOT self-invoke on import because SvelteKit runs layout modules
// in SSR context too, and `navigator` / `serviceWorker` are not available there.
//
// The Foundation / Library agent that owns +layout.svelte should add a single
// `onMount` call in that file:
//
//   import { bootstrapPWA } from '$lib/pwa/bootstrap';
//   onMount(bootstrapPWA);
//
// `bootstrapPWA` is safe to call multiple times — it guards with a flag so the
// registration and storage request each run at most once per page load.
//
// If +layout.svelte cannot be touched, an alternative is to call `bootstrapPWA()`
// from any top-level route's `onMount` (e.g. the library page), since it is
// idempotent. The function is intentionally lightweight and fast.

import { requestPersistentStorage } from '$lib/sync/persist';
import { signalUpdateAvailable } from '$lib/pwa/state.svelte';

let bootstrapped = false;

/**
 * Register the SvelteKit service worker and request persistent storage.
 * Must be called from a browser-only context (e.g. `onMount`).
 * Safe to call multiple times — executes the work exactly once per page load.
 */
export async function bootstrapPWA(): Promise<void> {
	if (bootstrapped) return;
	bootstrapped = true;

	if (typeof window === 'undefined') return;

	await Promise.all([registerServiceWorker(), requestPersistentStorage()]);
}

// ---------------------------------------------------------------------------
// Service worker registration
// ---------------------------------------------------------------------------

async function registerServiceWorker(): Promise<void> {
	if (!('serviceWorker' in navigator)) return;

	try {
		const registration = await navigator.serviceWorker.register('/service-worker.js', {
			// 'module' type is required for SvelteKit's built SW (ES module output).
			type: 'module'
		});

		// A new version may already be waiting (e.g. the user reopened the app after
		// it was downloaded last time) — `updatefound` won't fire for it, so surface
		// it explicitly. A worker only enters "waiting" while an active worker still
		// controls clients, so its mere presence already means this is an update, not
		// a first install — no extra guard needed.
		if (registration.waiting) {
			signalUpdateAvailable(registration.waiting);
		}

		registration.addEventListener('updatefound', () => {
			const installing = registration.installing;
			if (!installing) return;

			installing.addEventListener('statechange', () => {
				// `registration.active` distinguishes an update (an older worker is
				// already active) from a first install (no active worker yet at the
				// "installed" step). We deliberately avoid `navigator.serviceWorker.
				// controller` here: on iOS/WebKit it is often null mid-session even
				// when the page is controlled, which would suppress the banner.
				if (installing.state === 'installed' && registration.active) {
					signalUpdateAvailable(installing);
				}
			});
		});

		// For long-lived SPA sessions the browser won't re-check the SW on its own
		// until 24 h have passed. Poll every hour so updates surface promptly.
		setInterval(() => registration.update(), 60 * 60 * 1000);
	} catch (err) {
		// SW registration failure is non-fatal — the app still works online.
		console.warn('[PWA] Service worker registration failed:', err);
	}
}

// ---------------------------------------------------------------------------
// Manual update check
// ---------------------------------------------------------------------------

/**
 * Trigger an explicit SW update check. Returns true if a new version was found
 * (the UpdateBanner will appear), false if already on the latest version.
 */
export async function checkForUpdate(): Promise<boolean> {
	if (!('serviceWorker' in navigator)) return false;

	const registration = await navigator.serviceWorker.getRegistration();
	if (!registration) return false;

	// Already downloaded by an earlier check (hourly poll / previous visit). A
	// worker only waits while an active one still controls clients, so this is
	// always a genuine update.
	if (registration.waiting) {
		signalUpdateAvailable(registration.waiting);
		return true;
	}

	// Re-fetch the SW script. `update()` resolves once the browser has finished
	// the check; we then inspect the registration directly instead of waiting for
	// the `updatefound` event, which is unreliable on iOS/WebKit (it may not fire,
	// or may fire before a listener is attached).
	try {
		await registration.update();
	} catch {
		return false;
	}

	return surfaceDownloadedWorker(registration);
}

/**
 * After an `update()` check, surface a freshly downloaded worker: signal the
 * banner and return true once one is waiting, false if none appears. A worker
 * may still be installing, so we wait for it to reach the "installed" state.
 */
async function surfaceDownloadedWorker(registration: ServiceWorkerRegistration): Promise<boolean> {
	if (registration.waiting) {
		signalUpdateAvailable(registration.waiting);
		return true;
	}

	// `registration.active` guards against treating a first install as an update.
	const installing = registration.installing;
	if (!installing || !registration.active) return false;

	return new Promise((resolve) => {
		const timeout = setTimeout(() => resolve(false), 10_000);
		installing.addEventListener('statechange', () => {
			if (installing.state === 'installed') {
				clearTimeout(timeout);
				signalUpdateAvailable(installing);
				resolve(true);
			} else if (installing.state === 'redundant') {
				clearTimeout(timeout);
				resolve(false);
			}
		});
	});
}
