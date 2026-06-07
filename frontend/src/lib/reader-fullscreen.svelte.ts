// Immersive reading mode — hides the app chrome and requests the browser's
// native Fullscreen API. Named exports only; no default export.

import { browser } from '$app/environment';

// `active` drives the app chrome (the layout hides its header when set).
export const readerFullscreen = $state({ active: false });

// Leaving native fullscreen (e.g. the user presses Esc) should also leave
// immersive mode, so the two stay in sync.
if (browser) {
	document.addEventListener('fullscreenchange', () => {
		if (!document.fullscreenElement && readerFullscreen.active) {
			readerFullscreen.active = false;
		}
	});
}

export async function enterReaderFullscreen(): Promise<void> {
	readerFullscreen.active = true;
	if (!browser || document.fullscreenElement) return;
	// Best-effort: the Fullscreen API can reject (no user gesture, unsupported,
	// iOS Safari). Hiding the chrome is the primary effect and still applies.
	try {
		await document.documentElement.requestFullscreen();
	} catch {
		// Native fullscreen unavailable — immersive chrome-hiding still works.
	}
}

export async function exitReaderFullscreen(): Promise<void> {
	readerFullscreen.active = false;
	if (!browser || !document.fullscreenElement) return;
	try {
		await document.exitFullscreen();
	} catch {
		// Already out of native fullscreen — nothing more to do.
	}
}
