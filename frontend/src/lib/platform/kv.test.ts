import { describe, it, expect, vi, beforeEach } from 'vitest';

// The KV mirror is the recovery mechanism after a WKWebView storage eviction
// (MOBILE-ARCH.md §6.5, D4): if its round-trip is wrong, an evicted user is
// logged out and loses their server URL — the exact failure it exists to prevent.
// We mock ./index (platform switch) and @capacitor/preferences (native store).

const native = { value: true };
vi.mock('./index', () => ({ isNative: () => native.value }));

const backing = new Map<string, string>();
const setSpy = vi.fn(async ({ key, value }: { key: string; value: string }) => {
	backing.set(key, value);
});
const getSpy = vi.fn(async ({ key }: { key: string }) => ({ value: backing.get(key) ?? null }));
vi.mock('@capacitor/preferences', () => ({
	Preferences: {
		set: (opts: { key: string; value: string }) => setSpy(opts),
		get: (opts: { key: string }) => getSpy(opts)
	}
}));

import { mirrorSyncState, readMirror } from './kv';

beforeEach(() => {
	native.value = true;
	backing.clear();
	setSpy.mockClear();
	getSpy.mockClear();
});

describe('web (non-native)', () => {
	it('mirrorSyncState is a no-op and readMirror returns null', async () => {
		native.value = false;
		await mirrorSyncState({ authToken: 't', serverUrl: 's' });
		expect(setSpy).not.toHaveBeenCalled();
		expect(await readMirror()).toBeNull();
		expect(getSpy).not.toHaveBeenCalled();
	});
});

describe('native round-trip', () => {
	it('mirrors and reads back the recovery fields', async () => {
		await mirrorSyncState({ authToken: 't', serverUrl: 'https://s', cursor: 'c1' });
		expect(await readMirror()).toEqual({ authToken: 't', serverUrl: 'https://s', cursor: 'c1' });
	});

	it('clears a field (logout drops authToken) instead of preserving a stale value', async () => {
		await mirrorSyncState({ authToken: 't', serverUrl: 'https://s' });
		await mirrorSyncState({ authToken: undefined, serverUrl: 'https://s' });
		const mirror = await readMirror();
		expect(mirror?.authToken).toBeUndefined();
		expect(mirror?.serverUrl).toBe('https://s');
	});

	it('returns null when nothing has been mirrored', async () => {
		expect(await readMirror()).toBeNull();
	});

	it('returns null on a corrupt mirror value rather than throwing', async () => {
		backing.set('deep-reader:sync-state-mirror', '{not json');
		expect(await readMirror()).toBeNull();
	});
});
