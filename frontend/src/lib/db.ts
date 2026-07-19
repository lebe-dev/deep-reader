// Client-side IndexedDB cache (Dexie). Mirrors a subset of the server model for
// offline reading (deep-reader-architecture.md §6, §8 "Client (IndexedDB)").
//
// The local store is a CACHE, never the source of truth — losing it must not
// lose user data (spec §3 "Восстановимость").

import Dexie, { type Table } from 'dexie';
import { captureError, captureWarning } from './sentry';
import { mirrorSyncState } from './platform/kv';
import type {
	ArticleMeta,
	ArticlePayload,
	MarkdownBudget,
	Progress,
	ServerInfo,
	Settings,
	SettingsPatch,
	ProgressUpdate,
	ReEnrichMode
} from './types';

// ---------------------------------------------------------------------------
// Outbox — buffered writes flushed FIFO when the network returns (spec §10).
// ---------------------------------------------------------------------------

/** Discriminator for a queued offline write. */
export type OutboxKind =
	| 'progress'
	| 'settings'
	| 'add_article'
	| 'add_text'
	| 'delete_article'
	| 'retry'
	| 'reenrich'
	| 'pin';

/** Payload shapes keyed by outbox kind. */
export interface OutboxPayloadMap {
	progress: { article_id: string } & ProgressUpdate;
	settings: SettingsPatch;
	add_article: { url: string };
	add_text: { text: string; title: string; url?: string };
	delete_article: { id: string };
	retry: { id: string };
	reenrich: { id: string; mode: ReEnrichMode };
	pin: { id: string; pinned: boolean };
}

/** A single queued offline write. `id` is the auto-increment insertion order. */
export interface OutboxEntry<K extends OutboxKind = OutboxKind> {
	id?: number;
	kind: K;
	payload: OutboxPayloadMap[K];
	created_at: string;
}

// ---------------------------------------------------------------------------
// Sync state — singleton row holding the sync cursor and device config.
// ---------------------------------------------------------------------------

/** The fixed primary key of the sync-state singleton row. */
export const SYNC_STATE_ID = 'singleton' as const;

export interface SyncState {
	/** Always `SYNC_STATE_ID`. */
	id: typeof SYNC_STATE_ID;
	/** Last sync cursor returned by `GET /api/config`. */
	cursor?: string;
	/** Backend base URL (used when the PWA is not served by the backend). */
	serverUrl?: string;
	/** Session bearer token obtained from login/setup; sent as Authorization. */
	authToken?: string;
	/** Locally cached copy of the latest settings. */
	settings?: Settings;
	/** Locally cached markdown.new budget from the last successful pull. */
	markdownBudget?: MarkdownBudget;
	/** Locally cached non-secret server config from the last successful pull. */
	serverInfo?: ServerInfo;
}

// ---------------------------------------------------------------------------
// Database
// ---------------------------------------------------------------------------

/** Current schema version. Bump this whenever {@link STORES} changes. */
export const SCHEMA_VERSION = 1;

/**
 * Store/index definitions for {@link SCHEMA_VERSION}. Frozen and exported so a
 * regression test can pin the shape: a future index change that is not paired
 * with a {@link SCHEMA_VERSION} bump (+ a `.version(N).upgrade(...)` step) would
 * make Dexie refuse to open an existing DB (`VersionError`) and brick the app
 * for returning users. The test guards that any edit here is deliberate.
 */
export const STORES = Object.freeze({
	// Primary key `id`; secondary indexes for library views.
	articles_meta: 'id, status, created_at, updated_at',
	// Heavy immutable payloads, keyed by article id.
	articles_payload: 'id',
	// Reading progress, keyed by article id.
	progress: 'article_id, updated_at',
	// Auto-increment id preserves FIFO insertion order; index by kind.
	outbox: '++id, kind, created_at',
	// Singleton config / cursor row.
	sync_state: 'id'
} as const);

export class DeepReaderDB extends Dexie {
	articles_meta!: Table<ArticleMeta, string>;
	articles_payload!: Table<ArticlePayload, string>;
	progress!: Table<Progress, string>;
	outbox!: Table<OutboxEntry, number>;
	sync_state!: Table<SyncState, string>;

	constructor() {
		super('deep-reader');
		this.version(SCHEMA_VERSION).stores({ ...STORES });
	}
}

/** The shared Dexie database instance. */
export const db = new DeepReaderDB();

// ---------------------------------------------------------------------------
// IndexedDB / Dexie error capture
// ---------------------------------------------------------------------------

/**
 * Run an IndexedDB operation, reporting any failure (quota exceeded, eviction,
 * blocked/aborted upgrade, transaction abort) to Sentry tagged `{area:'idb'}`
 * before re-throwing. The data layer is otherwise invisible in production: the
 * sync engine and UI catch or `console.warn`-swallow these errors, so without a
 * deliberate capture here a stuck/failing local store leaves no signal. We
 * re-throw so existing caller behaviour (optimistic mutation rollback, retry on
 * the next sync) is preserved unchanged.
 *
 * `op` names the failing operation so events can be grouped without leaking any
 * payload contents (which may include pasted article text — never sent here).
 */
async function withIdbCapture<T>(
	op: string,
	run: () => Promise<T>,
	extra?: Record<string, unknown>
): Promise<T> {
	try {
		return await run();
	} catch (err) {
		captureError(err, {
			area: 'idb',
			extra: { op, name: err instanceof Error ? err.name : typeof err, ...extra }
		});
		throw err;
	}
}

/**
 * Report that the browser denied persistent storage (`navigator.storage.persist()
 * === false`). Without persistence the local cache is eviction-eligible under
 * pressure, which on iOS in particular can silently drop the offline library; a
 * warning (not an error — it is a browser policy decision, not a bug) gives an
 * aggregate signal of how often users run unprotected. No-op unless Sentry is
 * configured (production), so callers can invoke it unconditionally.
 */
export function reportPersistDenied(): void {
	captureWarning('persistent storage denied (persist() === false)', { area: 'idb' });
}

// ---------------------------------------------------------------------------
// Sync-state helpers
// ---------------------------------------------------------------------------

/** Read the sync-state singleton (creating an empty one if absent). */
export async function getSyncState(): Promise<SyncState> {
	return withIdbCapture('getSyncState', async () => {
		const existing = await db.sync_state.get(SYNC_STATE_ID);
		if (existing) return existing;
		const fresh: SyncState = { id: SYNC_STATE_ID };
		await db.sync_state.put(fresh);
		return fresh;
	});
}

/** Merge a partial update into the sync-state singleton. */
export async function updateSyncState(patch: Partial<Omit<SyncState, 'id'>>): Promise<SyncState> {
	return withIdbCapture('updateSyncState', async () => {
		const current = await getSyncState();
		const next: SyncState = { ...current, ...patch, id: SYNC_STATE_ID };
		await db.sync_state.put(next);
		// Mirror the recovery-critical fields to native KV so they survive an
		// IndexedDB eviction (§6.5, D4). No-op on web. Fire-and-forget: a mirror
		// failure must not fail the write — report it for triage instead.
		void mirrorSyncState({
			authToken: next.authToken,
			serverUrl: next.serverUrl,
			cursor: next.cursor
		}).catch((err) => captureError(err, { area: 'kv', extra: { op: 'mirrorSyncState' } }));
		return next;
	});
}

/**
 * Append a write to the outbox, preserving FIFO order. A failure here is the
 * most consequential IDB error in the app — the queued add/settings/delete/
 * re-enrich write is lost before it is ever persisted — so it is captured with
 * the outbox `kind` for triage before re-throwing.
 */
export async function enqueueOutbox<K extends OutboxKind>(
	kind: K,
	payload: OutboxPayloadMap[K]
): Promise<number> {
	return withIdbCapture(
		'enqueueOutbox',
		async () => db.outbox.add({ kind, payload, created_at: new Date().toISOString() }),
		{ kind }
	);
}
