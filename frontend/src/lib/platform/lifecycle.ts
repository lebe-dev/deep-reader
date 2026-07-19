// App lifecycle abstraction (MOBILE-ARCH.md §6.4).
//
//   - web    → window `focus` event (unchanged from the current behaviour).
//   - native → `@capacitor/app` `resume`. Returning from the background is the
//              primary sync trigger on mobile: the 60-second foreground interval
//              does not tick while the WebView is frozen by the OS.
//
// Named exports only; no default export.

import { browser } from '$app/environment';
import { isNative } from './index';

/**
 * Run `cb` when the app comes to the foreground; returns an unsubscribe
 * function. No-op (inert unsubscribe) off-browser.
 */
export function onForeground(cb: () => void): () => void {
	if (!browser) return () => {};

	if (!isNative()) {
		const handleFocus = () => cb();
		window.addEventListener('focus', handleFocus);
		return () => window.removeEventListener('focus', handleFocus);
	}

	// Native: addListener resolves to a handle asynchronously; guard against an
	// unsubscribe that races the import/registration.
	let cancelled = false;
	let remove: (() => void) | undefined;
	import('@capacitor/app').then(({ App }) => {
		if (cancelled) return;
		const handle = App.addListener('resume', () => cb());
		remove = () => void handle.then((h) => h.remove());
	});
	return () => {
		cancelled = true;
		remove?.();
	};
}
