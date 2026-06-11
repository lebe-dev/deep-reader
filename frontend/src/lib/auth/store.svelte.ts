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
	ApiError,
	OfflineError
} from '$lib/api';
import { getSyncState, updateSyncState } from '$lib/db';
import { captureError, clearSentryUser, initSentry, setSentryUser } from '$lib/sentry';

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
 * Whether an error from a best-effort, server-touching action is "expected" and
 * therefore should be swallowed silently rather than reported to Sentry.
 *
 * - `OfflineError` is routine (the user is simply offline).
 * - A 4xx response means the server was reached and answered deterministically
 *   (e.g. logging out with an already-revoked token returns 401); that is the
 *   action's normal failure mode, not a systemic backend problem.
 *
 * Everything else — 5xx, unexpected exceptions — is captured so a systemic
 * backend issue surfacing across many users produces an aggregate signal.
 */
function isExpectedActionError(err: unknown): boolean {
	if (err instanceof OfflineError) return true;
	if (err instanceof ApiError) return err.status >= 400 && err.status < 500;
	return false;
}

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
	// Associate subsequent Sentry events with this user (single-user app, no PII).
	setSentryUser(res.username);
}

/** Verify credentials, store the returned token, mark as authenticated. */
export async function login(username: string, password: string): Promise<void> {
	const res = await apiLogin(username, password);
	await updateSyncState({ authToken: res.token });
	authState.initialized = true;
	authState.authenticated = true;
	authState.username = res.username;
	// Associate subsequent Sentry events with this user (single-user app, no PII).
	setSentryUser(res.username);
}

/** End the session server-side (best-effort) and clear the local token. */
export async function logout(): Promise<void> {
	try {
		await apiLogout();
	} catch (err) {
		// Best-effort: even if the call fails, drop the local token below. Offline
		// and expected 4xx (e.g. an already-revoked token) are routine and stay
		// silent; anything else (5xx, unexpected) is captured so a systemic backend
		// problem hitting many users produces an aggregate signal.
		if (!isExpectedActionError(err)) {
			captureError(err, { area: 'auth', extra: { action: 'logout' } });
		}
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
	// Stop associating future Sentry events with the (now logged-out) user.
	clearSentryUser();
}
