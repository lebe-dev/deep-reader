// Native "is a backend configured?" gate (MOBILE-ARCH.md §7).
//
// On web the app is served by the backend and is same-origin, so there is always
// an implicit server — nothing to configure. On native there is no origin:
// `serverUrl` MUST be supplied before any auth/network work, otherwise requests
// would be issued against `capacitor://localhost`. The root layout consults this
// store to route a first-run native user to `/connect` before `/login`.
//
// Named exports only; no default export.

import { browser } from '$app/environment';
import { isNative } from '$lib/platform';
import { getSyncState, updateSyncState } from '$lib/db';
import { readMirror } from '$lib/platform/kv';

export interface ConnectState {
	/** True once the initial serverUrl check has completed (always immediate on web). */
	ready: boolean;
	/** Whether a backend is reachable-by-config: same-origin on web, serverUrl on native. */
	serverConfigured: boolean;
}

// Web needs no configuration, so it starts ready and configured. Native starts
// un-ready until `initConnect()` reads the stored serverUrl.
export const connectState = $state<ConnectState>({
	ready: !isNative(),
	serverConfigured: !isNative()
});

/** Resolve the native serverUrl gate at startup. No-op (already ready) on web. */
export async function initConnect(): Promise<void> {
	if (!browser || !isNative()) {
		connectState.ready = true;
		connectState.serverConfigured = true;
		return;
	}
	try {
		await recoverFromMirror();
		const state = await getSyncState();
		connectState.serverConfigured = !!state.serverUrl;
	} finally {
		connectState.ready = true;
	}
}

/**
 * Restore auth token + server URL from the native KV mirror when IndexedDB came
 * back empty — i.e. after a WKWebView storage eviction (§6.5, D4). The cursor is
 * deliberately reset so the next sync is a FULL sync that reconciles server-side
 * deletions and repopulates the (evicted) library, rather than a delta from a
 * stale cursor. No-op when IndexedDB still holds state or the mirror is empty.
 */
async function recoverFromMirror(): Promise<void> {
	const state = await getSyncState();
	if (state.serverUrl || state.authToken) return; // local store intact — nothing to recover

	const mirror = await readMirror();
	if (!mirror?.authToken || !mirror.serverUrl) return; // nothing worth restoring

	await updateSyncState({
		authToken: mirror.authToken,
		serverUrl: mirror.serverUrl,
		cursor: undefined
	});
}

/** Mark the backend configured once the user has entered and verified a serverUrl. */
export function markServerConfigured(): void {
	connectState.serverConfigured = true;
}
