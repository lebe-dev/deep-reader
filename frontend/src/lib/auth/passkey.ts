// Passkey (WebAuthn) ceremonies.
//
// Each ceremony is two round trips: "begin" returns a challenge and an opaque
// ceremony id, the browser (or the native shim) talks to the authenticator, and
// "finish" sends the answer back with that id. The wire conversion lives in
// ./webauthn; the platform bridge lives in $lib/platform/passkey.
//
// Named exports only; no default export.

import {
	beginPasskeyLogin,
	beginPasskeyRegistration,
	finishPasskeyLogin,
	finishPasskeyRegistration
} from '$lib/api';
import { updateSyncState } from '$lib/db';
import { initPasskeys } from '$lib/platform/passkey';
import { setSentryUser } from '$lib/sentry';
import type { PasskeyView } from '$lib/types';

import { authState } from './store.svelte';
import {
	serializeAssertion,
	serializeAttestation,
	toCreationOptions,
	toRequestOptions
} from './webauthn';

/**
 * Thrown when the authenticator produced nothing — the ceremony was dismissed,
 * or the platform returned a credential type we cannot use. Callers treat it as
 * "nothing happened" rather than as an error to report.
 */
export class PasskeyAbortedError extends Error {
	constructor(message = 'The passkey prompt was dismissed') {
		super(message);
		this.name = 'PasskeyAbortedError';
	}
}

/**
 * Register a new passkey for the signed-in account.
 *
 * Requires an authenticated session: a passkey is added to an account the user
 * already controls, never as a way of claiming one.
 */
export async function registerPasskey(name: string): Promise<PasskeyView> {
	await requirePasskeySupport();

	const challenge = await beginPasskeyRegistration();
	const credential = await navigator.credentials.create(toCreationOptions(challenge.options));
	if (!credential) throw new PasskeyAbortedError();

	return finishPasskeyRegistration(
		challenge.ceremony_id,
		serializeAttestation(credential as PublicKeyCredential),
		name
	);
}

/**
 * Sign in with a passkey.
 *
 * The challenge is discoverable, so no username is collected: the authenticator
 * offers the resident credential itself. On success the session token is stored
 * exactly as a password login would store it, and the caller can navigate.
 */
export async function loginWithPasskey(): Promise<void> {
	await requirePasskeySupport();

	const challenge = await beginPasskeyLogin();
	const credential = await navigator.credentials.get(toRequestOptions(challenge.options));
	if (!credential) throw new PasskeyAbortedError();

	const res = await finishPasskeyLogin(
		challenge.ceremony_id,
		serializeAssertion(credential as PublicKeyCredential)
	);

	await updateSyncState({ authToken: res.token });
	authState.initialized = true;
	authState.authenticated = true;
	authState.username = res.username;
	// Associate subsequent Sentry events with this user (single-user app, no PII).
	setSentryUser(res.username);
}

/**
 * Ensure a credential API is available, installing the native shim if needed.
 * Throws rather than letting `navigator.credentials` blow up with an opaque
 * TypeError on a platform that has none.
 */
async function requirePasskeySupport(): Promise<void> {
	const supported = await initPasskeys();
	if (!supported) throw new Error('Passkeys are not available on this device');
}
