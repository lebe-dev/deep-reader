// KV mirror of recovery-critical sync state (MOBILE-ARCH.md §6.5, D4).
//
//   - web    → no-op (IndexedDB is the single source of truth).
//   - native → `@capacitor/preferences` (iOS UserDefaults / Android
//              SharedPreferences), which survives a WKWebView storage eviction
//              that would otherwise drop the Dexie DB. After eviction the app can
//              restore its auth token + server URL from here and full-sync,
//              instead of logging the user out and losing the server setting.
//
// Named exports only; no default export.

import { isNative } from './index';

/** The subset of sync-state that must survive an IndexedDB eviction. */
export interface MirroredState {
	authToken?: string;
	serverUrl?: string;
	cursor?: string;
}

const MIRROR_KEY = 'deep-reader:sync-state-mirror';

/**
 * Persist the recovery-critical fields to native KV. No-op on web. The full
 * current values are passed (not a delta), so a cleared field — e.g. authToken
 * on logout — clears in the mirror too. `undefined` fields drop out of the JSON.
 */
export async function mirrorSyncState(s: MirroredState): Promise<void> {
	if (!isNative()) return;
	const { Preferences } = await import('@capacitor/preferences');
	const value = JSON.stringify({
		authToken: s.authToken,
		serverUrl: s.serverUrl,
		cursor: s.cursor
	});
	await Preferences.set({ key: MIRROR_KEY, value });
}

/** Read the mirrored state, or null on web / when nothing has been mirrored. */
export async function readMirror(): Promise<MirroredState | null> {
	if (!isNative()) return null;
	const { Preferences } = await import('@capacitor/preferences');
	const { value } = await Preferences.get({ key: MIRROR_KEY });
	if (!value) return null;
	try {
		return JSON.parse(value) as MirroredState;
	} catch {
		return null;
	}
}
