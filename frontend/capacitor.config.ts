import type { CapacitorConfig } from '@capacitor/cli';

// Capacitor wraps the SvelteKit static bundle (webDir: 'build') into the native
// iOS/Android shells. The web PWA is unaffected — see MOBILE-ARCH.md §3 (D5).
//
// server.url is intentionally NOT set: the frontend loads from the local
// in-container bundle, and all API traffic goes to the remote backend the user
// configures at runtime (sync_state.serverUrl), reaching it via CapacitorHttp to
// bypass CORS (D2). For dev live-reload, temporarily point server.url at the Vite
// dev server (e.g. `npm run dev -- --host`) and re-run `npx cap sync`.
// Passkeys: the WebAuthn relying-party domain has to be baked into the native
// projects at build time — iOS puts it in the Associated Domains entitlement and
// Android in an asset_statements resource, and neither can change at runtime.
// It therefore comes from PASSKEY_RP_ID in .env (the same variable the backend
// uses), not from the serverUrl the user types on /connect. Leave it unset and
// the plugin strips both wirings, which is what a web-only deployment wants.
const passkeyDomain = process.env.PASSKEY_RP_ID?.trim();

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
		// Spread rather than a plain key: an empty domain list makes the plugin's
		// cap-sync hook remove the native wiring, and we want it absent entirely
		// rather than present-but-empty.
		...(passkeyDomain
			? {
					CapacitorPasskey: {
						// iOS 17.4+ encodes this origin into clientDataJSON, which is how
						// the server's origin check passes for a native caller.
						origin: `https://${passkeyDomain}`,
						domains: [passkeyDomain]
					}
				}
			: {}),
		SplashScreen: {
			// Hidden programmatically once the app has bootstrapped (see platform
			// layer), so it never lingers on a ready WebView.
			launchAutoHide: false,
			backgroundColor: '#0a0a0a'
		}
	}
};

export default config;
