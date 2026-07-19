import type { CapacitorConfig } from '@capacitor/cli';

// Capacitor wraps the SvelteKit static bundle (webDir: 'build') into the native
// iOS/Android shells. The web PWA is unaffected — see MOBILE-ARCH.md §3 (D5).
//
// server.url is intentionally NOT set: the frontend loads from the local
// in-container bundle, and all API traffic goes to the remote backend the user
// configures at runtime (sync_state.serverUrl), reaching it via CapacitorHttp to
// bypass CORS (D2). For dev live-reload, temporarily point server.url at the Vite
// dev server (e.g. `npm run dev -- --host`) and re-run `npx cap sync`.
const config: CapacitorConfig = {
	appId: 'ru.tinyops.deepreader',
	appName: 'Deep Reader',
	webDir: 'build',
	ios: {
		// Opaque background while the WebView boots; avoids a white flash under the
		// splash on dark theme.
		backgroundColor: '#0a0a0a'
	},
	android: {
		backgroundColor: '#0a0a0a'
	},
	plugins: {
		SplashScreen: {
			// Hidden programmatically once the app has bootstrapped (see platform
			// layer), so it never lingers on a ready WebView.
			launchAutoHide: false,
			backgroundColor: '#0a0a0a'
		}
	}
};

export default config;
