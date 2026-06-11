import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Provide a real (in-memory) IndexedDB to the node test environment BEFORE the
// db module instantiates Dexie at import time. fake-indexeddb is a pure-JS impl
// that needs no DOM, so it works under the `node` vitest environment we use.
import { IDBFactory, IDBKeyRange } from 'fake-indexeddb';

// Mock the Sentry capture layer so we can assert {area:'idb'} reporting without
// a real SDK/transport. db.ts imports captureError/captureWarning from here.
const captureError = vi.fn();
const captureWarning = vi.fn();
vi.mock('./sentry', () => ({ captureError, captureWarning }));

type DbModule = typeof import('./db');

// A fresh in-memory IndexedDB + a freshly-imported db module per test, so each
// test starts from an empty store and a Dexie instance bound to that store.
async function freshDb(): Promise<DbModule> {
	vi.resetModules();
	captureError.mockClear();
	captureWarning.mockClear();
	// New backing store each time — isolates persisted rows between tests.
	vi.stubGlobal('indexedDB', new IDBFactory());
	vi.stubGlobal('IDBKeyRange', IDBKeyRange);
	return import('./db');
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('schema / version', () => {
	it('declares version 1 with no upgrade path (guards a future bricking change)', async () => {
		const { SCHEMA_VERSION } = await freshDb();
		// If this fails, a store/index change was made: add `.version(N).upgrade()`
		// in db.ts and bump SCHEMA_VERSION here so existing DBs open instead of
		// throwing VersionError for returning users.
		expect(SCHEMA_VERSION).toBe(1);
	});

	it('pins the store/index definitions — any change must be paired with a version bump', async () => {
		const { STORES } = await freshDb();
		// Pin the exact shape. Editing the schema in db.ts forces a deliberate
		// update here AND a SCHEMA_VERSION bump + upgrade step (see the test above).
		expect(STORES).toEqual({
			articles_meta: 'id, status, created_at, updated_at',
			articles_payload: 'id',
			progress: 'article_id, updated_at',
			outbox: '++id, kind, created_at',
			sync_state: 'id'
		});
	});

	it('opens against an empty IndexedDB at the declared version', async () => {
		const { db, SCHEMA_VERSION } = await freshDb();
		await db.open();
		expect(db.verno).toBe(SCHEMA_VERSION);
		expect(db.tables.map((t) => t.name).sort()).toEqual([
			'articles_meta',
			'articles_payload',
			'outbox',
			'progress',
			'sync_state'
		]);
		db.close();
	});
});

describe('getSyncState', () => {
	it('creates and returns an empty singleton on first read', async () => {
		const { getSyncState, SYNC_STATE_ID, db } = await freshDb();
		const state = await getSyncState();
		expect(state).toEqual({ id: SYNC_STATE_ID });
		// Persisted, not just returned.
		const stored = await db.sync_state.get(SYNC_STATE_ID);
		expect(stored).toEqual({ id: SYNC_STATE_ID });
	});

	it('returns the existing singleton without overwriting it', async () => {
		const { getSyncState, updateSyncState } = await freshDb();
		await updateSyncState({ authToken: 'tok', cursor: '2026-06-10T00:00:00Z' });
		const state = await getSyncState();
		expect(state.authToken).toBe('tok');
		expect(state.cursor).toBe('2026-06-10T00:00:00Z');
	});

	it('reports a read failure to Sentry tagged area:idb and re-throws', async () => {
		const { getSyncState, db } = await freshDb();
		const boom = new Error('quota exceeded');
		boom.name = 'QuotaExceededError';
		vi.spyOn(db.sync_state, 'get').mockRejectedValueOnce(boom);

		await expect(getSyncState()).rejects.toBe(boom);
		expect(captureError).toHaveBeenCalledTimes(1);
		expect(captureError).toHaveBeenCalledWith(
			boom,
			expect.objectContaining({
				area: 'idb',
				extra: expect.objectContaining({ op: 'getSyncState', name: 'QuotaExceededError' })
			})
		);
	});
});

describe('updateSyncState', () => {
	it('merges a partial patch and preserves the fixed id', async () => {
		const { updateSyncState, SYNC_STATE_ID } = await freshDb();
		await updateSyncState({ authToken: 'a', cursor: 'c1' });
		const next = await updateSyncState({ cursor: 'c2' });
		expect(next).toEqual({ id: SYNC_STATE_ID, authToken: 'a', cursor: 'c2' });
	});

	it('ignores an id in the patch (always pins SYNC_STATE_ID)', async () => {
		const { updateSyncState, SYNC_STATE_ID, db } = await freshDb();
		// Patch type forbids `id`, but a caller bug should still not fork the row.
		await updateSyncState({ authToken: 'x', id: 'rogue' } as never);
		const rows = await db.sync_state.toArray();
		expect(rows).toHaveLength(1);
		expect(rows[0].id).toBe(SYNC_STATE_ID);
	});

	it('reports a write failure to Sentry tagged area:idb and re-throws', async () => {
		const { updateSyncState, db } = await freshDb();
		const boom = new Error('blocked');
		vi.spyOn(db.sync_state, 'put').mockRejectedValueOnce(boom);

		await expect(updateSyncState({ cursor: 'c' })).rejects.toBe(boom);
		expect(captureError).toHaveBeenCalledWith(
			boom,
			expect.objectContaining({
				area: 'idb',
				extra: expect.objectContaining({ op: 'updateSyncState' })
			})
		);
	});
});

describe('enqueueOutbox', () => {
	it('appends entries in FIFO insertion order with a created_at timestamp', async () => {
		const { enqueueOutbox, db } = await freshDb();
		const id1 = await enqueueOutbox('add_article', { url: 'https://a' });
		const id2 = await enqueueOutbox('delete_article', { id: 'x' });
		expect(id2).toBeGreaterThan(id1);

		const entries = await db.outbox.orderBy('id').toArray();
		expect(entries.map((e) => e.kind)).toEqual(['add_article', 'delete_article']);
		expect(entries[0].payload).toEqual({ url: 'https://a' });
		expect(typeof entries[0].created_at).toBe('string');
		expect(Number.isNaN(Date.parse(entries[0].created_at))).toBe(false);
	});

	it('reports a failure to Sentry with the outbox kind and re-throws (data-loss path)', async () => {
		const { enqueueOutbox, db } = await freshDb();
		const boom = new Error('disk full');
		boom.name = 'QuotaExceededError';
		vi.spyOn(db.outbox, 'add').mockRejectedValueOnce(boom);

		await expect(
			enqueueOutbox('progress', {
				article_id: 'a1',
				position: 5,
				is_read: false,
				updated_at: 'now'
			})
		).rejects.toBe(boom);
		expect(captureError).toHaveBeenCalledWith(
			boom,
			expect.objectContaining({
				area: 'idb',
				extra: expect.objectContaining({ op: 'enqueueOutbox', kind: 'progress' })
			})
		);
	});
});

describe('reportPersistDenied', () => {
	beforeEach(() => {
		captureWarning.mockClear();
	});

	it('captures a warning tagged area:idb (persist() === false)', async () => {
		const { reportPersistDenied } = await freshDb();
		reportPersistDenied();
		expect(captureWarning).toHaveBeenCalledTimes(1);
		const [msg, ctx] = captureWarning.mock.calls[0];
		expect(typeof msg).toBe('string');
		expect(ctx).toEqual(expect.objectContaining({ area: 'idb' }));
	});
});
