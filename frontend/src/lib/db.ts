// Client-side IndexedDB cache (Dexie). Mirrors a subset of the server model for
// offline reading (deep-reader-architecture.md §6, §8 "Client (IndexedDB)").
//
// The local store is a CACHE, never the source of truth — losing it must not
// lose user data (spec §3 "Восстановимость").

import Dexie, { type Table } from 'dexie';
import type {
	ArticleMeta,
	ArticlePayload,
	MarkdownBudget,
	Progress,
	ServerInfo,
	Settings,
	SettingsPatch,
	ProgressUpdate
} from './types';

// ---------------------------------------------------------------------------
// Outbox — buffered writes flushed FIFO when the network returns (spec §10).
// ---------------------------------------------------------------------------

/** Discriminator for a queued offline write. */
export type OutboxKind =
	| 'progress'
	| 'settings'
	| 'add_article'
	| 'delete_article'
	| 'retry'
	| 'pin';

/** Payload shapes keyed by outbox kind. */
export interface OutboxPayloadMap {
	progress: { article_id: string } & ProgressUpdate;
	settings: SettingsPatch;
	add_article: { url: string };
	delete_article: { id: string };
	retry: { id: string };
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

export class DeepReaderDB extends Dexie {
	articles_meta!: Table<ArticleMeta, string>;
	articles_payload!: Table<ArticlePayload, string>;
	progress!: Table<Progress, string>;
	outbox!: Table<OutboxEntry, number>;
	sync_state!: Table<SyncState, string>;

	constructor() {
		super('deep-reader');

		this.version(1).stores({
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
		});
	}
}

/** The shared Dexie database instance. */
export const db = new DeepReaderDB();

// ---------------------------------------------------------------------------
// Sync-state helpers
// ---------------------------------------------------------------------------

/** Read the sync-state singleton (creating an empty one if absent). */
export async function getSyncState(): Promise<SyncState> {
	const existing = await db.sync_state.get(SYNC_STATE_ID);
	if (existing) return existing;
	const fresh: SyncState = { id: SYNC_STATE_ID };
	await db.sync_state.put(fresh);
	return fresh;
}

/** Merge a partial update into the sync-state singleton. */
export async function updateSyncState(patch: Partial<Omit<SyncState, 'id'>>): Promise<SyncState> {
	const current = await getSyncState();
	const next: SyncState = { ...current, ...patch, id: SYNC_STATE_ID };
	await db.sync_state.put(next);
	return next;
}

/** Append a write to the outbox, preserving FIFO order. */
export async function enqueueOutbox<K extends OutboxKind>(
	kind: K,
	payload: OutboxPayloadMap[K]
): Promise<number> {
	return db.outbox.add({ kind, payload, created_at: new Date().toISOString() });
}
