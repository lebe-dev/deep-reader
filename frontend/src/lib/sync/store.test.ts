// Unit tests for the reactive sync-status store (store.svelte.ts).
//
// The standalone vitest config does NOT load the Svelte compiler, so the `$state`
// rune used at module scope is provided as a lightweight identity shim (a plain
// object proxy is sufficient for these tests, which only read/write properties).
//
// $lib/db and ./engine are mocked so no real IndexedDB / network is touched.
// `window`, `navigator`, and timers are stubbed so the lifecycle wiring
// (listeners, interval, Dexie hook, cleanup) and the runSync reentrancy guard
// can be exercised deterministically.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// ---------------------------------------------------------------------------
// Mocks (module-scoped so the dynamic re-import per test picks them up)
// ---------------------------------------------------------------------------

let browserFlag = true;
vi.mock('$app/environment', () => ({
	get browser() {
		return browserFlag;
	}
}));

// Controllable sync() — each call returns a pending promise we resolve manually,
// so we can observe in-flight behavior (the reentrancy guard).
const sync = vi.fn<() => Promise<void>>();
vi.mock('./engine', () => ({
	get sync() {
		return sync;
	}
}));

const outboxCount = vi.fn<() => Promise<number>>();
const hookUnsubscribe = vi.fn();
// `hook` is callable (subscribe form) AND exposes `.creating.unsubscribe`.
const hook = Object.assign(vi.fn(), {
	creating: { unsubscribe: hookUnsubscribe }
});
const getSyncState = vi.fn<() => Promise<{ id: string; markdownBudget?: unknown }>>();

vi.mock('$lib/db', () => ({
	db: {
		outbox: {
			count: outboxCount,
			hook
		}
	},
	getSyncState
}));

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type StoreModule = typeof import('./store.svelte');

/** A deferred promise whose resolution we control. */
function deferred(): { promise: Promise<void>; resolve: () => void; reject: (e: unknown) => void } {
	let resolve!: () => void;
	let reject!: (e: unknown) => void;
	const promise = new Promise<void>((res, rej) => {
		resolve = res;
		reject = rej;
	});
	return { promise, resolve, reject };
}

/** Captured window event listeners, so tests can fire them. */
type Listeners = Record<string, Array<() => void>>;

function stubWindow(): Listeners {
	const listeners: Listeners = {};
	vi.stubGlobal('window', {
		addEventListener(type: string, fn: () => void) {
			(listeners[type] ??= []).push(fn);
		},
		removeEventListener(type: string, fn: () => void) {
			listeners[type] = (listeners[type] ?? []).filter((f) => f !== fn);
		}
	});
	return listeners;
}

function fire(listeners: Listeners, type: string): void {
	for (const fn of listeners[type] ?? []) fn();
}

/** Flush pending microtasks so awaited promise chains settle. */
async function flush(): Promise<void> {
	await Promise.resolve();
	await Promise.resolve();
	await Promise.resolve();
}

async function loadModule(): Promise<StoreModule> {
	vi.resetModules();
	return import('./store.svelte');
}

// ---------------------------------------------------------------------------
// Suite
// ---------------------------------------------------------------------------

beforeEach(() => {
	browserFlag = true;
	sync.mockReset();
	outboxCount.mockReset().mockResolvedValue(0);
	hook.mockReset();
	hookUnsubscribe.mockReset();
	getSyncState.mockReset().mockResolvedValue({ id: 'singleton' });

	// `$state` rune shim — identity is enough for property read/write assertions.
	vi.stubGlobal('$state', <T>(v: T): T => v);
	// Default online navigator; individual tests may override.
	vi.stubGlobal('navigator', { onLine: true });
});

afterEach(() => {
	vi.unstubAllGlobals();
	vi.useRealTimers();
});

describe('initSync lifecycle wiring', () => {
	it('is a no-op (returns inert cleanup) when not in the browser', async () => {
		browserFlag = false;
		stubWindow();
		const mod = await loadModule();

		const cleanup = mod.initSync();

		expect(sync).not.toHaveBeenCalled();
		expect(hook).not.toHaveBeenCalled();
		// Cleanup must be safe to call.
		expect(() => cleanup()).not.toThrow();
	});

	it('registers online/offline/focus listeners, the interval, and the Dexie hook', async () => {
		const listeners = stubWindow();
		const setIntervalSpy = vi.spyOn(globalThis, 'setInterval');
		sync.mockResolvedValue(undefined);
		const mod = await loadModule();

		mod.initSync();

		expect(listeners.online).toHaveLength(1);
		expect(listeners.offline).toHaveLength(1);
		expect(listeners.focus).toHaveLength(1);
		expect(hook).toHaveBeenCalledWith('creating', expect.any(Function));
		expect(setIntervalSpy).toHaveBeenCalledTimes(1);
		// Initial sync on mount.
		expect(sync).toHaveBeenCalledTimes(1);
	});

	it('seeds online state from navigator and the markdown budget from sync_state', async () => {
		stubWindow();
		vi.stubGlobal('navigator', { onLine: false });
		const budget = { remaining: 7, limit: 10 };
		getSyncState.mockResolvedValue({ id: 'singleton', markdownBudget: budget });
		sync.mockResolvedValue(undefined);
		const mod = await loadModule();

		mod.initSync();
		await flush();

		expect(mod.syncStatus.online).toBe(false);
		expect(mod.syncStatus.markdownBudget).toEqual(budget);
	});

	it('cleanup removes every listener, clears the interval, and unsubscribes the Dexie hook', async () => {
		const listeners = stubWindow();
		const clearIntervalSpy = vi.spyOn(globalThis, 'clearInterval');
		sync.mockResolvedValue(undefined);
		const mod = await loadModule();

		const cleanup = mod.initSync();
		// Capture the exact hook subscriber so we can assert it's the one removed.
		const subscriber = hook.mock.calls[0][1];

		cleanup();

		expect(listeners.online).toHaveLength(0);
		expect(listeners.offline).toHaveLength(0);
		expect(listeners.focus).toHaveLength(0);
		expect(clearIntervalSpy).toHaveBeenCalledTimes(1);
		expect(hookUnsubscribe).toHaveBeenCalledWith(subscriber);
	});

	it('the foreground interval runs a sync only while online', async () => {
		vi.useFakeTimers();
		stubWindow();
		sync.mockResolvedValue(undefined);
		const mod = await loadModule();

		mod.initSync();
		await flush();
		expect(sync).toHaveBeenCalledTimes(1); // initial mount sync

		// Online tick → syncs.
		mod.syncStatus.online = true;
		vi.advanceTimersByTime(60_000);
		expect(sync).toHaveBeenCalledTimes(2);

		// Offline tick → skipped.
		mod.syncStatus.online = false;
		vi.advanceTimersByTime(60_000);
		expect(sync).toHaveBeenCalledTimes(2);
	});
});

describe('event handlers drive sync state', () => {
	it('online event sets online=true and triggers a sync; offline sets online=false', async () => {
		const listeners = stubWindow();
		sync.mockResolvedValue(undefined);
		const mod = await loadModule();

		mod.initSync();
		await flush();
		sync.mockClear();

		fire(listeners, 'offline');
		expect(mod.syncStatus.online).toBe(false);
		expect(sync).not.toHaveBeenCalled();

		fire(listeners, 'online');
		expect(mod.syncStatus.online).toBe(true);
		expect(sync).toHaveBeenCalledTimes(1);
	});

	it('focus event triggers a sync', async () => {
		const listeners = stubWindow();
		sync.mockResolvedValue(undefined);
		const mod = await loadModule();

		mod.initSync();
		await flush();
		sync.mockClear();

		fire(listeners, 'focus');
		expect(sync).toHaveBeenCalledTimes(1);
	});
});

describe('runSync reentrancy guard', () => {
	it('coalesces overlapping runs: a second trigger while in flight does not re-enter sync()', async () => {
		const listeners = stubWindow();
		const first = deferred();
		sync.mockReturnValueOnce(first.promise);
		const mod = await loadModule();

		mod.initSync(); // initial sync → in flight (first.promise pending)
		await flush();
		expect(sync).toHaveBeenCalledTimes(1);

		// While the first run is still in flight, a focus event must NOT start
		// another sync (the syncInFlight guard short-circuits).
		fire(listeners, 'focus');
		fire(listeners, 'focus');
		await flush();
		expect(sync).toHaveBeenCalledTimes(1);

		// Resolve the in-flight run; the guard releases.
		sync.mockResolvedValue(undefined);
		first.resolve();
		await flush();

		// A subsequent trigger now starts a fresh sync.
		fire(listeners, 'focus');
		await flush();
		expect(sync).toHaveBeenCalledTimes(2);
	});

	it('stamps lastSyncedAt from a local clock on success (not the undefined server cursor)', async () => {
		stubWindow();
		sync.mockResolvedValue(undefined);
		// getSyncState has no cursor — the old code bound lastSyncedAt to it and
		// the indicator never appeared. A local timestamp must be used instead.
		getSyncState.mockResolvedValue({ id: 'singleton', markdownBudget: { remaining: 1 } });
		outboxCount.mockResolvedValue(3);
		const before = Date.now();
		const mod = await loadModule();

		mod.initSync();
		await flush();

		expect(mod.syncStatus.lastSyncedAt).toBeDefined();
		const stamped = Date.parse(mod.syncStatus.lastSyncedAt as string);
		expect(stamped).toBeGreaterThanOrEqual(before);
		expect(mod.syncStatus.pending).toBe(3);
		expect(mod.syncStatus.markdownBudget).toEqual({ remaining: 1 });
		expect(mod.syncStatus.error).toBeUndefined();
	});

	it('records the error and preserves lastSyncedAt when a later sync rejects', async () => {
		const listeners = stubWindow();
		// First run (on mount) succeeds and stamps lastSyncedAt; the next rejects.
		sync.mockResolvedValueOnce(undefined);
		const mod = await loadModule();

		mod.initSync();
		await flush();
		const stamped = mod.syncStatus.lastSyncedAt;
		expect(stamped).toBeDefined();
		expect(mod.syncStatus.error).toBeUndefined();

		// Re-trigger runSync via the focus listener with a rejecting sync.
		sync.mockRejectedValueOnce(new Error('boom'));
		fire(listeners, 'focus');
		await flush();

		expect(mod.syncStatus.error).toBe('boom');
		// A failed sync must not clear the last successful timestamp.
		expect(mod.syncStatus.lastSyncedAt).toBe(stamped);
	});

	it('clears a stale error at the start of a successful run', async () => {
		const listeners = stubWindow();
		sync.mockRejectedValueOnce(new Error('first failure'));
		const mod = await loadModule();

		mod.initSync();
		await flush();
		expect(mod.syncStatus.error).toBe('first failure');

		sync.mockResolvedValueOnce(undefined);
		fire(listeners, 'focus');
		await flush();

		expect(mod.syncStatus.error).toBeUndefined();
	});
});
