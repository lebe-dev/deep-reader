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
		// it explicitly. The controller check ensures we don't prompt on first install.
		if (registration.waiting && navigator.serviceWorker.controller) {
			signalUpdateAvailable(registration.waiting);
		}

		registration.addEventListener('updatefound', () => {
			const installing = registration.installing;
			if (!installing) return;

			installing.addEventListener('statechange', () => {
				if (installing.state === 'installed' && navigator.serviceWorker.controller) {
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

	if (registration.waiting && navigator.serviceWorker.controller) {
		signalUpdateAvailable(registration.waiting);
		return true;
	}

	return new Promise((resolve) => {
		let settled = false;
		const settle = (found: boolean) => {
			if (settled) return;
			settled = true;
			resolve(found);
		};

		const timeout = setTimeout(() => settle(false), 10_000);

		registration.addEventListener(
			'updatefound',
			() => {
				clearTimeout(timeout);
				settle(true);
			},
			{ once: true }
		);

		registration.update().catch(() => {
			clearTimeout(timeout);
			settle(false);
		});
	});
}
