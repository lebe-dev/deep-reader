// Platform layer — passkeys (WebAuthn) on web and native.
//
// A Capacitor app loads its page from `capacitor://localhost`, so the browser's
// own WebAuthn implementation would bind credentials to that origin, not to the
// Deep Reader server. `@capgo/capacitor-passkey` bridges to the platform APIs
// (AuthenticationServices on iOS, Credential Manager on Android) and installs a
// shim over `navigator.credentials.create/get`, so the rest of the app keeps one
// code path for both targets.
//
// The native side additionally needs the OS to trust the app for the RP's
// domain: iOS reads /.well-known/apple-app-site-association (and the domain must
// be listed in the app's Associated Domains entitlement, which is baked in at
// build time from PASSKEY_RP_ID), Android reads /.well-known/assetlinks.json.
// Both files are served by the backend from the same PASSKEY_* variables.
//
// This is the only module outside the rest of lib/platform that touches a
// Capacitor plugin — everything else calls the helpers here.
//
// Named exports only; no default export.

import { CapacitorPasskey } from '@capgo/capacitor-passkey';

import { captureError } from '../sentry';
import { isNative } from './index';

/** Resolves once the native shim has been installed (or found unavailable). */
let shimReady: Promise<boolean> | undefined;

/**
 * Install the native WebAuthn shim, once per app run.
 *
 * On the web this is a no-op: the browser already implements
 * `navigator.credentials`. Returns whether passkeys can be used at all, so the
 * UI can hide the affordance rather than offer a button that throws.
 */
export async function initPasskeys(): Promise<boolean> {
	shimReady ??= installShim();
	return shimReady;
}

async function installShim(): Promise<boolean> {
	if (!isNative()) return hasCredentialApi();

	try {
		// Reads `plugins.CapacitorPasskey` from capacitor.config.ts — which the
		// build stamps from PASSKEY_RP_ID — and patches navigator.credentials to
		// route through the platform's own passkey UI.
		await CapacitorPasskey.autoShimWebAuthn();
		return hasCredentialApi();
	} catch (err) {
		// A missing entitlement or an unconfigured domain is a deployment problem,
		// not a user error: report it, and let the UI fall back to the password
		// form rather than failing at the moment the user taps the button.
		captureError(err, { area: 'auth', extra: { action: 'passkey-shim' } });
		return false;
	}
}

/**
 * Whether a credential API is present. Checked after the shim is installed, so
 * on native it reflects the shim rather than the WebView's own support.
 */
function hasCredentialApi(): boolean {
	return (
		typeof navigator !== 'undefined' &&
		typeof navigator.credentials?.create === 'function' &&
		typeof navigator.credentials?.get === 'function' &&
		typeof PublicKeyCredential !== 'undefined'
	);
}

/**
 * Whether the user cancelled the platform's passkey prompt (or it timed out).
 *
 * Both arrive as a `NotAllowedError`, and neither is a failure worth showing an
 * error for — the user simply chose not to continue.
 */
export function isPasskeyCancellation(err: unknown): boolean {
	if (!(err instanceof Error)) return false;
	return err.name === 'NotAllowedError' || err.name === 'AbortError';
}
