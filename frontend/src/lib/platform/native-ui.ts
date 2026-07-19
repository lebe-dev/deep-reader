// Native shell UX concerns (MOBILE-ARCH.md §12): splash screen, status bar
// theming, and the Android hardware back button. All no-ops on web.
//
// Named exports only; no default export.

import { isNative, platform } from './index';

/**
 * Initialise native chrome once the WebView is up. Hides the splash screen
 * (which is configured not to auto-hide, so the app owns the moment it appears)
 * and wires the Android back button to in-app navigation. Safe/no-op on web.
 */
export async function initNativeUI(): Promise<void> {
	if (!isNative()) return;

	// Hide the splash first and independently — a failure wiring the back button
	// must never leave the splash covering a ready app.
	try {
		const { SplashScreen } = await import('@capacitor/splash-screen');
		await SplashScreen.hide();
	} catch {
		// Splash hiding is best-effort; the WebView is already interactive.
	}

	const { App } = await import('@capacitor/app');
	// Android hardware back: walk the SPA history; only leave the app from the
	// root screen. iOS has no hardware back, so this listener simply never fires.
	App.addListener('backButton', ({ canGoBack }) => {
		if (canGoBack || window.history.length > 1) {
			window.history.back();
			return;
		}
		void App.exitApp();
	});
}

/**
 * Match the native status bar to the active reading theme. `dark` selects a dark
 * status bar (light glyphs); otherwise a light one (dark glyphs). The Android
 * background colour is set to match; iOS overlays and needs only the style.
 */
export async function syncStatusBar(dark: boolean): Promise<void> {
	if (!isNative()) return;
	try {
		const { StatusBar, Style } = await import('@capacitor/status-bar');
		await StatusBar.setStyle({ style: dark ? Style.Dark : Style.Light });
		if (platform() === 'android') {
			await StatusBar.setBackgroundColor({ color: dark ? '#0a0a0a' : '#ffffff' });
		}
	} catch {
		// The status bar is cosmetic; never let a failure here break startup.
	}
}
