// Unit tests for the sync engine: pull() reconciliation (cursor contract,
// delete-only-on-full-sync, pending-delete exclusion, progress LWW, payload
// freshness/cache-bust), flushOutbox() error-class branching (Offline / 401 /
// 4xx / 5xx) with Sentry signalling, and the enqueue helpers' optimistic local
// mutations + status mappings.
//
// The engine is framework-agnostic, so we mock its three collaborators —
// `$lib/db` (an in-memory fake of the Dexie tables it touches), `$lib/api`, and
// `$lib/auth/store.svelte` — plus `$lib/sentry` to assert telemetry.

import { describe, it, expect, vi, beforeEach } from 'vitest';
import type { ArticleMeta, ArticlePayload, Progress, Settings } from '$lib/types';
import type { OutboxEntry, OutboxKind, SyncState } from '$lib/db';

// ---------------------------------------------------------------------------
// In-memory Dexie-shaped fake. Defined inside vi.hoisted so the hoisted
// vi.mock('$lib/db') factory (lifted above normal top-level code) can build the
// tables. The engine reads the current tables through the live `state.db`
// object, so reassigning them per test is transparent to `import { db }`.
// ---------------------------------------------------------------------------

const h = vi.hoisted(() => {
	/** A minimal table backed by a Map, exposing the methods the engine calls. */
	class FakeTable<T extends object = Record<string, unknown>> {
		rows = new Map<string | number, T>();
		constructor(private key: string) {}

		async get(id: string | number): Promise<T | undefined> {
			const v = this.rows.get(id);
			return v ? { ...v } : undefined;
		}
		async put(value: T): Promise<void> {
			this.rows.set((value as Record<string, unknown>)[this.key] as string | number, { ...value });
		}
		async bulkPut(values: T[]): Promise<void> {
			for (const v of values) await this.put(v);
		}
		async delete(id: string | number): Promise<void> {
			this.rows.delete(id);
		}
		async bulkDelete(ids: (string | number)[]): Promise<void> {
			for (const id of ids) this.rows.delete(id);
		}
		async toArray(): Promise<T[]> {
			return [...this.rows.values()].map((v) => ({ ...v }));
		}
		orderBy(field: string) {
			const get = (v: T) => (v as Record<string, unknown>)[field] as number;
			const sorted = [...this.rows.values()].sort((a, b) => (get(a) < get(b) ? -1 : 1));
			return { toArray: async () => sorted.map((v) => ({ ...v })) };
		}
		where(field: string) {
			return {
				equals: (val: unknown) => ({
					toArray: async () =>
						[...this.rows.values()]
							.filter((v) => (v as Record<string, unknown>)[field] === val)
							.map((v) => ({ ...v }))
				})
			};
		}
	}

	const state = {
		articles_meta: new FakeTable<ArticleMeta>('id'),
		articles_payload: new FakeTable<ArticlePayload>('id'),
		progress: new FakeTable<Progress>('article_id'),
		outbox: new FakeTable<OutboxEntry>('id'),
		sync_state: new FakeTable<SyncState>('id'),
		outboxSeq: 0
	};

	function makeDb() {
		state.articles_meta = new FakeTable<ArticleMeta>('id');
		state.articles_payload = new FakeTable<ArticlePayload>('id');
		state.progress = new FakeTable<Progress>('article_id');
		state.outbox = new FakeTable<OutboxEntry>('id');
		state.sync_state = new FakeTable<SyncState>('id');
		state.outboxSeq = 0;
	}

	// Live façade: every table getter resolves the current `state` table.
	const db = {
		get articles_meta() {
			return state.articles_meta;
		},
		get articles_payload() {
			return state.articles_payload;
		},
		get progress() {
			return state.progress;
		},
		get outbox() {
			return state.outbox;
		},
		get sync_state() {
			return state.sync_state;
		},
		// transaction(mode, tables, fn) — run fn immediately (no real isolation).
		transaction: async (_mode: string, _tables: unknown, fn: () => Promise<void>) => fn()
	};

	const getSyncState = vi.fn(async () => {
		const row = await state.sync_state.get('singleton');
		return row ?? ({ id: 'singleton' } as SyncState);
	});
	const updateSyncState = vi.fn(async (patch: Record<string, unknown>) => {
		const cur = (await state.sync_state.get('singleton')) ?? ({ id: 'singleton' } as SyncState);
		const next = { ...cur, ...patch, id: 'singleton' } as SyncState;
		await state.sync_state.put(next);
		return next;
	});
	const enqueueOutbox = vi.fn(async (kind: OutboxKind, payload: unknown) => {
		const id = ++state.outboxSeq;
		await state.outbox.put({
			id,
			kind,
			payload,
			created_at: new Date().toISOString()
		} as OutboxEntry);
		return id;
	});

	return { state, makeDb, db, getSyncState, updateSyncState, enqueueOutbox };
});

const { state, makeDb, updateSyncState } = h;

// Per-test table bindings, reassigned in beforeEach to the current tables. The
// mock factory reads the live tables through `h.db`, independent of these.
let articles_meta: (typeof state)['articles_meta'];
let articles_payload: (typeof state)['articles_payload'];
let progress: (typeof state)['progress'];
let outbox: (typeof state)['outbox'];
let sync_state: (typeof state)['sync_state'];

vi.mock('$lib/db', () => ({
	db: h.db,
	getSyncState: h.getSyncState,
	updateSyncState: h.updateSyncState,
	enqueueOutbox: h.enqueueOutbox,
	SYNC_STATE_ID: 'singleton'
}));

// ---------------------------------------------------------------------------
// API + auth + sentry mocks
// ---------------------------------------------------------------------------

// API / auth / sentry mocks — also hoisted so the lifted vi.mock factories can
// reference them.
const m = vi.hoisted(() => {
	class OfflineError extends Error {
		constructor(message = 'Network unavailable') {
			super(message);
			this.name = 'OfflineError';
		}
	}
	class ApiError extends Error {
		readonly status: number;
		readonly body: string;
		constructor(status: number, body = '') {
			super(`API error ${status}`);
			this.name = 'ApiError';
			this.status = status;
			this.body = body;
		}
	}
	return {
		OfflineError,
		ApiError,
		getConfig: vi.fn(),
		apiGetArticle: vi.fn(),
		apiAddArticle: vi.fn(),
		apiAddArticleText: vi.fn(),
		apiDeleteArticle: vi.fn(),
		apiRetryArticle: vi.fn(),
		apiReEnrichArticle: vi.fn(),
		apiPinArticle: vi.fn(),
		putProgress: vi.fn(),
		patchSettings: vi.fn(),
		clearSession: vi.fn(),
		captureError: vi.fn(),
		addSyncBreadcrumb: vi.fn()
	};
});

const {
	OfflineError,
	ApiError,
	getConfig,
	apiGetArticle,
	apiAddArticle,
	apiAddArticleText,
	apiDeleteArticle,
	apiRetryArticle,
	apiReEnrichArticle,
	apiPinArticle,
	putProgress,
	patchSettings,
	clearSession,
	captureError,
	addSyncBreadcrumb
} = m;

vi.mock('$lib/api', () => ({
	OfflineError: m.OfflineError,
	ApiError: m.ApiError,
	getConfig: m.getConfig,
	getArticle: m.apiGetArticle,
	addArticle: m.apiAddArticle,
	addArticleText: m.apiAddArticleText,
	deleteArticle: m.apiDeleteArticle,
	retryArticle: m.apiRetryArticle,
	reEnrichArticle: m.apiReEnrichArticle,
	pinArticle: m.apiPinArticle,
	putProgress: m.putProgress,
	patchSettings: m.patchSettings
}));

vi.mock('$lib/auth/store.svelte', () => ({ clearSession: m.clearSession }));

vi.mock('$lib/sentry', () => ({
	captureError: m.captureError,
	addSyncBreadcrumb: m.addSyncBreadcrumb
}));

// Import the system under test AFTER the mocks are registered.
import {
	pull,
	flushOutbox,
	enqueueProgress,
	enqueueDelete,
	enqueueRetry,
	enqueueReEnrich,
	isReEnrichPending,
	enqueuePin,
	enqueueSettings,
	enqueueAddArticle
} from './engine';

// ---------------------------------------------------------------------------
// Builders
// ---------------------------------------------------------------------------

function meta(over: Partial<ArticleMeta> & { id: string }): ArticleMeta {
	return {
		source_url: '',
		title: 't',
		author: '',
		source_domain: '',
		lang: 'en',
		status: 'enriched',
		pinned: false,
		enrichment_version: 1,
		created_at: '2026-01-01T00:00:00.000Z',
		updated_at: '2026-01-01T00:00:00.000Z',
		token_count: 10,
		enrichment_coverage: 1,
		...over
	};
}

function payload(id: string, over: Partial<ArticlePayload> = {}): ArticlePayload {
	return {
		id,
		original_text: 'body',
		tokens: [],
		enrichment: { difficult_words: [], phrases: [], sentences: [], glossary: [] },
		enrichment_version: 1,
		status: 'enriched',
		enrichment_coverage: 1,
		...over
	};
}

function configResponse(over: Record<string, unknown> = {}): Record<string, unknown> {
	return {
		auth: { initialized: true, authenticated: true },
		settings: { updated_at: 'srv' },
		articles: [],
		progress: [],
		markdown_budget: { enabled: false },
		server_info: {},
		sentry: { dsn: '', environment: '', release: '' },
		server_time: '2026-06-10T12:00:00Z',
		...over
	};
}

beforeEach(() => {
	makeDb();
	articles_meta = state.articles_meta;
	articles_payload = state.articles_payload;
	progress = state.progress;
	outbox = state.outbox;
	sync_state = state.sync_state;
	vi.clearAllMocks();
	// Re-prime default resolved values cleared by clearAllMocks.
	apiAddArticle.mockResolvedValue({ id: 'x', status: 'queued' });
	apiAddArticleText.mockResolvedValue({ id: 'x', status: 'queued' });
	apiDeleteArticle.mockResolvedValue(undefined);
	apiRetryArticle.mockResolvedValue({ id: 'x', status: 'queued' });
	apiReEnrichArticle.mockResolvedValue({ id: 'x', status: 'queued' });
	apiPinArticle.mockResolvedValue(undefined);
	putProgress.mockResolvedValue(undefined);
	patchSettings.mockImplementation(async (p: Record<string, unknown>) => ({
		...p,
		updated_at: 'srv'
	}));
	// Most enqueue tests run "offline" so sync() is not auto-triggered; opt in
	// per test by stubbing navigator.onLine.
	vi.stubGlobal('navigator', { onLine: false });
});

// ===========================================================================
// pull() — reconciliation
// ===========================================================================

describe('pull — cursor contract', () => {
	it('sends no `since` on the first (full) sync and persists server_time as the cursor', async () => {
		getConfig.mockResolvedValue(configResponse());
		await pull();
		expect(getConfig).toHaveBeenCalledWith(undefined);
		const row = await sync_state.get('singleton');
		expect(row!.cursor).toBe('2026-06-10T12:00:00Z');
	});

	it('sends the stored cursor as `since` on a subsequent (delta) sync', async () => {
		await sync_state.put({ id: 'singleton', cursor: '2026-06-10T12:00:00Z' });
		getConfig.mockResolvedValue(configResponse({ server_time: '2026-06-10T13:00:00Z' }));
		await pull();
		expect(getConfig).toHaveBeenCalledWith('2026-06-10T12:00:00Z');
		const row = await sync_state.get('singleton');
		expect(row!.cursor).toBe('2026-06-10T13:00:00Z');
	});

	it('prefers an explicit `cursor` field over server_time when present', async () => {
		getConfig.mockResolvedValue(configResponse({ cursor: 'explicit', server_time: 'time' }));
		await pull();
		const row = await sync_state.get('singleton');
		expect(row!.cursor).toBe('explicit');
	});
});

describe('pull — null arrays (Go nil slices)', () => {
	it('tolerates null articles/progress in the response without throwing', async () => {
		await articles_meta.put(meta({ id: 'existing' }));
		// A delta sync that changed nothing comes back with null arrays (Go marshals
		// empty slices as JSON null). pull() must not crash on .filter/.map/iteration.
		// Seed a cursor so this is a delta (not a full snapshot that prunes absences).
		await sync_state.put({ id: 'singleton', cursor: 'prev' } as never);
		getConfig.mockResolvedValue(
			configResponse({ cursor: 'c0', articles: null, progress: null, server_time: 'srv2' })
		);
		await expect(pull()).resolves.toBeUndefined();
		// Delta sync (cursor present) → untouched library is preserved, not wiped.
		expect(await articles_meta.get('existing')).toBeDefined();
		expect((await sync_state.get('singleton'))!.cursor).toBe('c0');
	});
});

describe('pull — delete reconciliation (data-loss hazard)', () => {
	it('deletes locally-present articles absent from a FULL server snapshot', async () => {
		await articles_meta.put(meta({ id: 'gone' }));
		await articles_payload.put(payload('gone'));
		await articles_meta.put(meta({ id: 'kept' }));
		getConfig.mockResolvedValue(configResponse({ articles: [meta({ id: 'kept' })] }));

		await pull();

		expect(await articles_meta.get('gone')).toBeUndefined();
		expect(await articles_payload.get('gone')).toBeUndefined();
		expect(await articles_meta.get('kept')).toBeDefined();
	});

	it('does NOT delete untouched local articles on a DELTA sync (cursor set)', async () => {
		// Cursor present → delta sync. The delta response carries only the one
		// changed article; the rest of the library must survive.
		await sync_state.put({ id: 'singleton', cursor: '2026-06-10T11:00:00Z' });
		await articles_meta.put(meta({ id: 'old1' }));
		await articles_meta.put(meta({ id: 'old2' }));
		getConfig.mockResolvedValue(
			configResponse({
				server_time: '2026-06-10T12:00:00Z',
				articles: [meta({ id: 'changed' })]
			})
		);

		await pull();

		expect(await articles_meta.get('old1')).toBeDefined();
		expect(await articles_meta.get('old2')).toBeDefined();
		expect(await articles_meta.get('changed')).toBeDefined();
	});

	it('does not resurrect an article pending deletion in the outbox', async () => {
		await outbox.put({
			id: 1,
			kind: 'delete_article',
			payload: { id: 'p' },
			created_at: 'c'
		} as never);
		getConfig.mockResolvedValue(
			configResponse({ articles: [meta({ id: 'p' }), meta({ id: 'q' })] })
		);

		await pull();

		expect(await articles_meta.get('p')).toBeUndefined();
		expect(await articles_meta.get('q')).toBeDefined();
	});

	it('does not delete a pending-delete article on a full sync even if absent from the response', async () => {
		// Locally present, queued for deletion, and absent from the server list.
		// It must be left to the outbox, not double-deleted by reconciliation.
		await articles_meta.put(meta({ id: 'p' }));
		await outbox.put({
			id: 1,
			kind: 'delete_article',
			payload: { id: 'p' },
			created_at: 'c'
		} as never);
		getConfig.mockResolvedValue(configResponse({ articles: [] }));

		await pull();
		// Reconciliation skips it (pendingDeleteIds excluded); still present until
		// the outbox flush confirms the server delete.
		expect(await articles_meta.get('p')).toBeDefined();
	});
});

describe('pull — progress LWW', () => {
	it('overwrites local progress when the server row is newer', async () => {
		await progress.put({
			article_id: 'a',
			position: 1,
			is_read: false,
			updated_at: '2026-01-01T00:00:00.000Z'
		});
		getConfig.mockResolvedValue(
			configResponse({
				progress: [
					{ article_id: 'a', position: 9, is_read: true, updated_at: '2026-01-02T00:00:00.000Z' }
				]
			})
		);
		await pull();
		expect((await progress.get('a'))!.position).toBe(9);
	});

	it('keeps the newer local progress when the server row is older', async () => {
		await progress.put({
			article_id: 'a',
			position: 9,
			is_read: true,
			updated_at: '2026-01-02T00:00:00.000Z'
		});
		getConfig.mockResolvedValue(
			configResponse({
				progress: [
					{ article_id: 'a', position: 1, is_read: false, updated_at: '2026-01-01T00:00:00.000Z' }
				]
			})
		);
		await pull();
		expect((await progress.get('a'))!.position).toBe(9);
	});

	it('compares parsed instants, not strings (ms vs RFC3339Nano precision)', async () => {
		// Lexicographically '...:00.999Z' > '...:01.000000001Z' (the dot/colon
		// mismatch), but the nano timestamp is the truly newer instant.
		await progress.put({
			article_id: 'a',
			position: 1,
			is_read: false,
			updated_at: '2026-01-01T00:00:00.999Z'
		});
		getConfig.mockResolvedValue(
			configResponse({
				progress: [
					{
						article_id: 'a',
						position: 7,
						is_read: true,
						updated_at: '2026-01-01T00:00:01.000000001Z'
					}
				]
			})
		);
		await pull();
		expect((await progress.get('a'))!.position).toBe(7);
	});
});

describe('pull — payload cache freshness', () => {
	it('fetches and stamps the payload for an enriched article with no cache', async () => {
		const m = meta({ id: 'a', enrichment_version: 3, updated_at: '2026-02-02T00:00:00Z' });
		getConfig.mockResolvedValue(configResponse({ articles: [m] }));
		apiGetArticle.mockResolvedValue(payload('a', { enrichment_version: 3 }));

		await pull();

		expect(apiGetArticle).toHaveBeenCalledWith('a', undefined, { version: '2026-02-02T00:00:00Z' });
		const cached = (await articles_payload.get('a')) as unknown as Record<string, unknown>;
		expect(cached._synced_updated_at).toBe('2026-02-02T00:00:00Z');
	});

	it('serves a fresh cache (same version AND same updated_at) without refetching', async () => {
		const m = meta({ id: 'a', enrichment_version: 3, updated_at: '2026-02-02T00:00:00Z' });
		await articles_payload.put({
			...payload('a', { enrichment_version: 3 }),
			_synced_updated_at: '2026-02-02T00:00:00Z'
		} as never);
		getConfig.mockResolvedValue(configResponse({ articles: [m] }));

		await pull();
		expect(apiGetArticle).not.toHaveBeenCalled();
	});

	it('refetches when updated_at advanced even though enrichment_version is unchanged (out-of-band re-enrich)', async () => {
		const m = meta({ id: 'a', enrichment_version: 3, updated_at: '2026-02-03T00:00:00Z' });
		// Stale cache: same version, OLDER updated_at signal.
		await articles_payload.put({
			...payload('a', { enrichment_version: 3 }),
			_synced_updated_at: '2026-02-02T00:00:00Z'
		} as never);
		getConfig.mockResolvedValue(configResponse({ articles: [m] }));
		apiGetArticle.mockResolvedValue(payload('a', { enrichment_version: 3, original_text: 'NEW' }));

		await pull();

		expect(apiGetArticle).toHaveBeenCalledWith('a', undefined, { version: '2026-02-03T00:00:00Z' });
		const cached = (await articles_payload.get('a')) as unknown as Record<string, unknown>;
		expect(cached.original_text).toBe('NEW');
		expect(cached._synced_updated_at).toBe('2026-02-03T00:00:00Z');
	});

	it('refetches a legacy cache that has a version but no freshness stamp', async () => {
		const m = meta({ id: 'a', enrichment_version: 3, updated_at: '2026-02-02T00:00:00Z' });
		await articles_payload.put(payload('a', { enrichment_version: 3 })); // no _synced_updated_at
		getConfig.mockResolvedValue(configResponse({ articles: [m] }));
		apiGetArticle.mockResolvedValue(payload('a', { enrichment_version: 3 }));

		await pull();
		expect(apiGetArticle).toHaveBeenCalledTimes(1);
	});

	it('stops the payload loop on OfflineError but does not throw', async () => {
		const a = meta({ id: 'a' });
		const b = meta({ id: 'b' });
		getConfig.mockResolvedValue(configResponse({ articles: [a, b] }));
		apiGetArticle.mockRejectedValue(new OfflineError());

		await expect(pull()).resolves.toBeUndefined();
		expect(apiGetArticle).toHaveBeenCalledTimes(1); // broke after the first
	});
});

describe('pull — session lifecycle', () => {
	it('clears the session and stops when the response is unauthenticated', async () => {
		getConfig.mockResolvedValue(
			configResponse({ auth: { initialized: true, authenticated: false } })
		);
		await articles_meta.put(meta({ id: 'a' }));

		await pull();

		expect(clearSession).toHaveBeenCalledOnce();
		// No reconciliation ran: the local article is untouched.
		expect(await articles_meta.get('a')).toBeDefined();
		expect(updateSyncState).not.toHaveBeenCalled();
	});
});

// ===========================================================================
// flushOutbox() — error-class branching
// ===========================================================================

function queue(kind: OutboxKind, payloadVal: unknown): Promise<void> {
	const id = ++state.outboxSeq;
	return outbox.put({ id, kind, payload: payloadVal, created_at: 'c' } as OutboxEntry);
}

describe('flushOutbox — branching', () => {
	it('drains FIFO and deletes each entry on success', async () => {
		await queue('add_article', { url: 'u1' });
		await queue('add_article', { url: 'u2' });

		await flushOutbox();

		expect(apiAddArticle).toHaveBeenNthCalledWith(1, 'u1');
		expect(apiAddArticle).toHaveBeenNthCalledWith(2, 'u2');
		expect((await outbox.toArray()).length).toBe(0);
	});

	it('stops on OfflineError and KEEPS the entry (and the rest)', async () => {
		await queue('add_article', { url: 'u1' });
		await queue('add_article', { url: 'u2' });
		apiAddArticle.mockRejectedValueOnce(new OfflineError());

		await flushOutbox();

		// First failed offline → both remain; second never attempted.
		expect((await outbox.toArray()).length).toBe(2);
		expect(apiAddArticle).toHaveBeenCalledTimes(1);
	});

	it('on 401 clears the session, KEEPS the entry, and stops', async () => {
		await queue('add_article', { url: 'u1' });
		await queue('add_article', { url: 'u2' });
		apiAddArticle.mockRejectedValueOnce(new ApiError(401, 'unauthorized'));

		await flushOutbox();

		expect(clearSession).toHaveBeenCalledOnce();
		expect((await outbox.toArray()).length).toBe(2);
		expect(apiAddArticle).toHaveBeenCalledTimes(1);
	});

	it('on a 4xx DROPS the entry, continues draining, and reports to Sentry with size-only payload', async () => {
		await queue('add_article', { url: 'https://secret.example/article' });
		await queue('add_article', { url: 'u2' });
		apiAddArticle.mockRejectedValueOnce(new ApiError(422, 'unprocessable'));

		await flushOutbox();

		// Dropped the bad one, processed the next → outbox empty.
		expect((await outbox.toArray()).length).toBe(0);
		expect(apiAddArticle).toHaveBeenCalledTimes(2);

		expect(captureError).toHaveBeenCalledOnce();
		const [err, ctx] = captureError.mock.calls[0];
		expect(err).toBeInstanceOf(ApiError);
		expect(ctx.area).toBe('sync');
		expect(ctx.extra.kind).toBe('add_article');
		expect(ctx.extra.http_status).toBe(422);
		// Redacted: only the key set + byte size, never the raw url value.
		expect(ctx.extra.payload_keys).toEqual(['url']);
		expect(typeof ctx.extra.payload_bytes).toBe('number');
		expect(JSON.stringify(ctx.extra)).not.toContain('secret.example');
	});

	it('on a 5xx KEEPS the entry, stops draining, and reports to Sentry', async () => {
		await queue('add_article', { url: 'u1' });
		await queue('add_article', { url: 'u2' });
		apiAddArticle.mockRejectedValueOnce(new ApiError(500, 'boom'));

		await flushOutbox();

		expect((await outbox.toArray()).length).toBe(2); // wedged, nothing dropped
		expect(apiAddArticle).toHaveBeenCalledTimes(1);
		expect(captureError).toHaveBeenCalledOnce();
		expect(captureError.mock.calls[0][1].extra.http_status).toBe(500);
	});

	it('adds a dispatch breadcrumb for every entry attempted', async () => {
		await queue('add_article', { url: 'u1' });
		await queue('pin', { id: 'a', pinned: true });

		await flushOutbox();

		const dispatchCrumbs = addSyncBreadcrumb.mock.calls.filter((c) => c[0] === 'outbox dispatch');
		expect(dispatchCrumbs.length).toBe(2);
	});

	it('skips a progress entry superseded by a newer local row (LWW)', async () => {
		await progress.put({
			article_id: 'a',
			position: 9,
			is_read: true,
			updated_at: '2026-01-02T00:00:00.000Z'
		});
		await queue('progress', {
			article_id: 'a',
			position: 1,
			is_read: false,
			updated_at: '2026-01-01T00:00:00.000Z'
		});

		await flushOutbox();

		expect(putProgress).not.toHaveBeenCalled();
		expect((await outbox.toArray()).length).toBe(0); // still removed from outbox
	});

	it('sends a progress entry that is newer than the local row', async () => {
		await progress.put({
			article_id: 'a',
			position: 1,
			is_read: false,
			updated_at: '2026-01-01T00:00:00.000Z'
		});
		await queue('progress', {
			article_id: 'a',
			position: 5,
			is_read: false,
			updated_at: '2026-01-03T00:00:00.000Z'
		});

		await flushOutbox();
		expect(putProgress).toHaveBeenCalledOnce();
	});

	it('persists the server-returned settings into sync_state on a settings flush', async () => {
		await queue('settings', { cefr_level: 'B2' });

		await flushOutbox();

		expect(patchSettings).toHaveBeenCalledWith({ cefr_level: 'B2' });
		const row = await sync_state.get('singleton');
		expect((row!.settings as unknown as Record<string, unknown>).cefr_level).toBe('B2');
	});

	it('dispatches each kind to its matching API call', async () => {
		await queue('add_text', { text: 'body', title: 'T', url: 'u' });
		await queue('delete_article', { id: 'd' });
		await queue('retry', { id: 'r' });
		await queue('reenrich', { id: 'e', mode: 'topup' });
		await queue('pin', { id: 'p', pinned: true });

		await flushOutbox();

		expect(apiAddArticleText).toHaveBeenCalledWith('body', 'T', 'u');
		expect(apiDeleteArticle).toHaveBeenCalledWith('d');
		expect(apiRetryArticle).toHaveBeenCalledWith('r');
		expect(apiReEnrichArticle).toHaveBeenCalledWith('e', 'topup');
		expect(apiPinArticle).toHaveBeenCalledWith('p', true);
	});
});

// ===========================================================================
// Enqueue helpers — optimistic mutations + status mappings
// ===========================================================================

describe('enqueueProgress', () => {
	it('writes the optimistic progress and queues a progress outbox entry', async () => {
		await enqueueProgress({
			article_id: 'a',
			position: 4,
			is_read: false,
			updated_at: '2026-01-02T00:00:00.000Z'
		});

		expect((await progress.get('a'))!.position).toBe(4);
		const entries = await outbox.toArray();
		expect(entries[0].kind).toBe('progress');
	});

	it('does not overwrite a strictly-newer local progress row', async () => {
		await progress.put({
			article_id: 'a',
			position: 9,
			is_read: true,
			updated_at: '2026-01-03T00:00:00.000Z'
		});
		await enqueueProgress({
			article_id: 'a',
			position: 1,
			is_read: false,
			updated_at: '2026-01-01T00:00:00.000Z'
		});
		// Optimistic write skipped (older), but the outbox entry is still queued.
		expect((await progress.get('a'))!.position).toBe(9);
		expect((await outbox.toArray())[0].kind).toBe('progress');
	});
});

describe('enqueueDelete', () => {
	it('optimistically removes meta/payload/progress and queues a delete', async () => {
		await articles_meta.put(meta({ id: 'a' }));
		await articles_payload.put(payload('a'));
		await progress.put({ article_id: 'a', position: 1, is_read: false, updated_at: 'x' });

		await enqueueDelete('a');

		expect(await articles_meta.get('a')).toBeUndefined();
		expect(await articles_payload.get('a')).toBeUndefined();
		expect(await progress.get('a')).toBeUndefined();
		expect((await outbox.toArray())[0]).toMatchObject({
			kind: 'delete_article',
			payload: { id: 'a' }
		});
	});
});

describe('enqueueRetry', () => {
	it('maps enrich_failed → fetched optimistically', async () => {
		await articles_meta.put(meta({ id: 'a', status: 'enrich_failed' }));
		await enqueueRetry('a');
		expect((await articles_meta.get('a'))!.status).toBe('fetched');
		expect((await outbox.toArray())[0].kind).toBe('retry');
	});

	it('maps any other failed status → queued optimistically', async () => {
		await articles_meta.put(meta({ id: 'a', status: 'fetch_failed' }));
		await enqueueRetry('a');
		expect((await articles_meta.get('a'))!.status).toBe('queued');
	});
});

describe('enqueueReEnrich', () => {
	it('topup → enriching and busts the cached payload', async () => {
		await articles_meta.put(meta({ id: 'a', status: 'enriched' }));
		await articles_payload.put(payload('a'));

		await enqueueReEnrich('a', 'topup');

		expect((await articles_meta.get('a'))!.status).toBe('enriching');
		expect(await articles_payload.get('a')).toBeUndefined();
		expect((await outbox.toArray())[0]).toMatchObject({
			kind: 'reenrich',
			payload: { id: 'a', mode: 'topup' }
		});
	});

	it('full → fetched and busts the cached payload', async () => {
		await articles_meta.put(meta({ id: 'a', status: 'enriched' }));
		await articles_payload.put(payload('a'));

		await enqueueReEnrich('a', 'full');

		expect((await articles_meta.get('a'))!.status).toBe('fetched');
		expect(await articles_payload.get('a')).toBeUndefined();
	});
});

describe('isReEnrichPending', () => {
	it('is true while a reenrich entry for the id is queued', async () => {
		await queue('reenrich', { id: 'a', mode: 'full' });
		expect(await isReEnrichPending('a')).toBe(true);
	});

	it('is false when no reenrich entry is queued for the id', async () => {
		expect(await isReEnrichPending('a')).toBe(false);
	});

	it('ignores reenrich entries for a different id and other kinds', async () => {
		await queue('reenrich', { id: 'other', mode: 'full' });
		await queue('retry', { id: 'a' });
		expect(await isReEnrichPending('a')).toBe(false);
	});
});

describe('enqueuePin', () => {
	it('optimistically toggles the local pinned flag and queues a pin', async () => {
		await articles_meta.put(meta({ id: 'a', pinned: false }));
		await enqueuePin('a', true);
		expect((await articles_meta.get('a'))!.pinned).toBe(true);
		expect((await outbox.toArray())[0]).toMatchObject({
			kind: 'pin',
			payload: { id: 'a', pinned: true }
		});
	});
});

describe('enqueueSettings', () => {
	it('optimistically merges into the cached settings and queues a patch', async () => {
		await sync_state.put({
			id: 'singleton',
			settings: { cefr_level: 'B1', updated_at: 'old' } as Settings
		});
		await enqueueSettings({ cefr_level: 'C1' });

		const row = await sync_state.get('singleton');
		expect((row!.settings as unknown as Record<string, unknown>).cefr_level).toBe('C1');
		expect((await outbox.toArray())[0].kind).toBe('settings');
	});
});

describe('enqueue helpers — online trigger', () => {
	it('triggers a sync when online (add_article round-trips through flush+pull)', async () => {
		vi.stubGlobal('navigator', { onLine: true });
		getConfig.mockResolvedValue(configResponse());

		await enqueueAddArticle('https://example.com');
		// The sync chain runs asynchronously; let microtasks drain.
		await new Promise((r) => setTimeout(r, 0));

		expect(apiAddArticle).toHaveBeenCalledWith('https://example.com');
		expect(getConfig).toHaveBeenCalled();
	});
});
