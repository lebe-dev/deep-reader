// Network status abstraction (MOBILE-ARCH.md §6.3).
//
//   - web    → `navigator.onLine` + window `online`/`offline` events.
//   - native → `@capacitor/network`. The WebView's own online/offline events are
//              unreliable on mobile (Wi-Fi ↔ cellular hand-off, wake-from-sleep),
//              so the native plugin is mandatory.
//
// Named exports only; no default export.

import { browser } from '$app/environment';
import { isNative } from './index';

/** Current connectivity. Resolves true off-browser (SSR) as a safe default. */
export async function isOnline(): Promise<boolean> {
	if (!browser) return true;
	if (!isNative()) return navigator.onLine;
	const { Network } = await import('@capacitor/network');
	const status = await Network.getStatus();
	return status.connected;
}

/**
 * Subscribe to connectivity changes; returns an unsubscribe function. `cb`
 * receives the new online state. No-op (inert unsubscribe) off-browser.
 */
export function onNetworkChange(cb: (online: boolean) => void): () => void {
	if (!browser) return () => {};

	if (!isNative()) {
		const handleOnline = () => cb(true);
		const handleOffline = () => cb(false);
		window.addEventListener('online', handleOnline);
		window.addEventListener('offline', handleOffline);
		return () => {
			window.removeEventListener('online', handleOnline);
			window.removeEventListener('offline', handleOffline);
		};
	}

	// Native: addListener resolves to a handle asynchronously; guard against an
	// unsubscribe that races the import/registration.
	let cancelled = false;
	let remove: (() => void) | undefined;
	import('@capacitor/network').then(({ Network }) => {
		if (cancelled) return;
		const handle = Network.addListener('networkStatusChange', (s) => cb(s.connected));
		remove = () => void handle.then((h) => h.remove());
	});
	return () => {
		cancelled = true;
		remove?.();
	};
}
