// Reactive authentication store (Svelte 5 runes).
//
// Holds the single-user auth state the root layout uses to route between
// /setup, /login and the app, and exposes the credential operations. The
// session token itself lives in db.sync_state.authToken (so api.ts can attach it
// as a Bearer header); this store only mirrors the *state*.

import {
	getConfig,
	login as apiLogin,
	logout as apiLogout,
	setup as apiSetup,
	OfflineError
} from '$lib/api';
import { getSyncState, updateSyncState } from '$lib/db';
import { initSentry } from '$lib/sentry';

export interface AuthState {
	/**
	 * Whether the service has a built-in account. `undefined` until the first
	 * `refreshAuth()` resolves.
	 */
	initialized?: boolean;
	/** Whether the current session token is valid. */
	authenticated: boolean;
	/** True once the first auth check (online or offline fallback) has completed. */
	checked: boolean;
	/** Logged-in username, when known. */
	username?: string;
}

export const authState = $state<AuthState>({ authenticated: false, checked: false });

/**
 * Re-evaluate auth state against the server. Uses `GET /api/config`, the only
 * endpoint reachable before login, which reports `{initialized, authenticated}`.
 *
 * Offline fallback: when the server is unreachable we keep the app usable from
 * the local cache, treating a stored token as "authenticated" so the user is not
 * bounced to /login while offline.
 */
export async function refreshAuth(): Promise<void> {
	try {
		const cfg = await getConfig();
		// Configure Sentry from the server-provided DSN as early as possible — this
		// is the first call on startup and runs before auth routing, so reporting is
		// live on /login and /setup too. Idempotent; no-op when the DSN is empty.
		initSentry(cfg.sentry);
		authState.initialized = cfg.auth.initialized;
		authState.authenticated = cfg.auth.authenticated;
	} catch (err) {
		if (err instanceof OfflineError) {
			const state = await getSyncState();
			authState.authenticated = !!state.authToken;
			// Assume initialized if we ever held a token; first online sync corrects it.
			authState.initialized = authState.initialized ?? authState.authenticated;
		}
		// Non-offline errors leave the last known state in place.
	} finally {
		authState.checked = true;
	}
}

/** Create the single account (first-run), store the returned token, log in. */
export async function setupAccount(username: string, password: string): Promise<void> {
	const res = await apiSetup(username, password);
	await updateSyncState({ authToken: res.token });
	authState.initialized = true;
	authState.authenticated = true;
	authState.username = res.username;
}

/** Verify credentials, store the returned token, mark as authenticated. */
export async function login(username: string, password: string): Promise<void> {
	const res = await apiLogin(username, password);
	await updateSyncState({ authToken: res.token });
	authState.initialized = true;
	authState.authenticated = true;
	authState.username = res.username;
}

/** End the session server-side (best-effort) and clear the local token. */
export async function logout(): Promise<void> {
	try {
		await apiLogout();
	} catch {
		// Best-effort: even if the network call fails, drop the local token below.
	}
	await clearSession();
}

/**
 * Drop the local session without a server round-trip. Called when a protected
 * request returns 401 (the session expired or was revoked server-side).
 */
export async function clearSession(): Promise<void> {
	await updateSyncState({ authToken: undefined });
	authState.authenticated = false;
	authState.username = undefined;
}
