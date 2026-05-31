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

		registration.addEventListener('updatefound', () => {
			const installing = registration.installing;
			if (!installing) return;

			installing.addEventListener('statechange', () => {
				if (installing.state === 'installed' && navigator.serviceWorker.controller) {
					signalUpdateAvailable(installing);
				}
			});
		});
	} catch (err) {
		// SW registration failure is non-fatal — the app still works online.
		console.warn('[PWA] Service worker registration failed:', err);
	}
}
