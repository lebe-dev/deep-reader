// Sync engine — pull/push logic, outbox drain, enqueue helpers.
// Spec §6 "Sync engine", §7 "Data flows", §10 "Offline-first".
//
// Named exports only; no default export.

import { db, enqueueOutbox, getSyncState, updateSyncState, type OutboxEntry } from '$lib/db';
import { clearSession } from '$lib/auth/store.svelte';
import {
	addArticle as apiAddArticle,
	addArticleText as apiAddArticleText,
	deleteArticle as apiDeleteArticle,
	getArticle as apiGetArticle,
	getConfig,
	OfflineError,
	ApiError,
	patchSettings,
	pinArticle as apiPinArticle,
	putProgress,
	reEnrichArticle as apiReEnrichArticle,
	retryArticle as apiRetryArticle
} from '$lib/api';
import { addSyncBreadcrumb, captureError } from '$lib/sentry';
import type {
	ArticlePayload,
	ConfigResponse,
	Progress,
	ProgressUpdate,
	ReEnrichMode,
	SettingsPatch
} from '$lib/types';

// ---------------------------------------------------------------------------
// Wire contract: the delta-sync cursor is the server's `server_time` (RFC3339).
// ---------------------------------------------------------------------------
//
// The backend GET /api/config emits `server_time` (its authoritative clock) and
// accepts `?since=<RFC3339>` to narrow the article/progress lists to records
// updated at or after that instant. Historically the client read a non-existent
// `cursor` field, so the cursor was permanently undefined: every pull was a full
// snapshot AND the delete reconciliation (remove local articles absent from the
// response) happened to be correct *because* the response was always complete.
//
// We now read `server_time` as the cursor. That makes pulls true deltas, which
// means absence from a delta response no longer implies server-side deletion —
// so the delete reconciliation must run ONLY on a full (no-`since`) sync. Doing
// one without the other risks mass article deletion (see the project memory note
// "sync-cursor / toDelete coupling").

/**
 * The server's sync cursor lives on the wire as `server_time` (an RFC3339
 * timestamp). The shared `ConfigResponse` type may still carry the legacy
 * optional `cursor`; prefer `cursor` when a future backend supplies it, else
 * fall back to `server_time`.
 */
function readCursor(response: ConfigResponse): string | undefined {
	const withTime = response as ConfigResponse & { server_time?: string };
	return response.cursor ?? withTime.server_time;
}

// Freshness signal stored alongside a cached payload so an out-of-band re-enrich
// (another device) that bumps `updated_at` without changing `enrichment_version`
// still busts the local cache. Stored as an untyped property on the payload row;
// Dexie persists arbitrary fields and the reader ignores unknown ones.
const PAYLOAD_FRESHNESS_KEY = '_synced_updated_at';
type CachedPayload = ArticlePayload & { [PAYLOAD_FRESHNESS_KEY]?: string };

/** Whether a cached payload is still fresh for the given server meta. */
function isPayloadFresh(
	cached: CachedPayload | undefined,
	meta: { enrichment_version: number; updated_at: string }
): boolean {
	if (!cached) return false;
	if (cached.enrichment_version !== meta.enrichment_version) return false;
	// A same-version re-enrich (other device) bumps updated_at but not the
	// version — compare the freshness signal so the stale payload is refetched.
	return cached[PAYLOAD_FRESHNESS_KEY] === meta.updated_at;
}

/** Parse an RFC3339/ISO timestamp to epoch ms; NaN sorts as oldest. */
function instant(ts: string | undefined): number {
	if (!ts) return Number.NEGATIVE_INFINITY;
	const ms = Date.parse(ts);
	return Number.isNaN(ms) ? Number.NEGATIVE_INFINITY : ms;
}

/**
 * Last-writer-wins comparison for progress timestamps. Compares parsed instants
 * rather than lexicographically: server timestamps can be RFC3339Nano while the
 * client emits millisecond ISO strings, and a raw string compare can invert the
 * true order across that precision mismatch.
 */
function isNewer(candidate: string | undefined, existing: string | undefined): boolean {
	return instant(candidate) > instant(existing);
}

// ---------------------------------------------------------------------------
// pull — server → local diff
// ---------------------------------------------------------------------------

/**
 * Fetch a delta from the server and apply it to the local IndexedDB.
 * - Upserts new / changed articles_meta.
 * - Removes articles deleted server-side — ONLY on a full (no-cursor) sync,
 *   where absence from the response truly means deletion. On a delta sync the
 *   response carries only changed records, so absence is not deletion.
 * - Upserts progress rows (LWW: keep whichever updated_at is newer).
 * - Updates settings singleton in sync_state.
 * - Advances the cursor to the server-returned `server_time`.
 * - For enriched articles whose cached payload is stale (version or updated_at
 *   changed) or absent, lazily fetches and caches the full payload.
 */
export async function pull(): Promise<void> {
	const state = await getSyncState();
	// Empty/undefined cursor → a full snapshot request (no ?since). Delete
	// reconciliation is only safe on this full request.
	const isFullSync = !state.cursor;
	addSyncBreadcrumb('pull start', { full: isFullSync });
	const response = await getConfig(state.cursor);

	// The session expired or was revoked server-side — drop it and stop. The
	// layout guard reacts to the cleared auth state and routes to /login.
	if (!response.auth?.authenticated) {
		await clearSession();
		return;
	}

	await db.transaction(
		'rw',
		[db.articles_meta, db.articles_payload, db.progress, db.sync_state, db.outbox],
		async () => {
			// --- articles_meta ---
			// Exclude articles that are pending deletion in the outbox — a concurrent
			// pull must not re-insert an article the user just deleted optimistically.
			const pendingDeletes = await db.outbox.where('kind').equals('delete_article').toArray();
			const pendingDeleteIds = new Set(pendingDeletes.map((e) => (e.payload as { id: string }).id));

			// Upsert server articles, skipping those pending local deletion.
			const articlesToUpsert = response.articles.filter((a) => !pendingDeleteIds.has(a.id));
			await db.articles_meta.bulkPut(articlesToUpsert);

			// Remove articles that are no longer on the server — ONLY on a full sync.
			// On a delta sync the response is the set of *changed* records, so an
			// article's absence means "unchanged", not "deleted"; deleting on a delta
			// would wipe the whole untouched library.
			if (isFullSync) {
				const serverIds = new Set(response.articles.map((a) => a.id));
				const localMetas = await db.articles_meta.toArray();
				// Only delete what was previously synced (not newly locally added
				// pending items, which are not yet on the server).
				const toDelete = localMetas
					.filter((a) => !serverIds.has(a.id) && !pendingDeleteIds.has(a.id))
					.map((a) => a.id);
				if (toDelete.length > 0) {
					await db.articles_meta.bulkDelete(toDelete);
					await db.articles_payload.bulkDelete(toDelete);
				}
			}

			// --- progress (LWW by parsed updated_at instant) ---
			for (const serverProg of response.progress) {
				const local = await db.progress.get(serverProg.article_id);
				if (!local || isNewer(serverProg.updated_at, local.updated_at)) {
					await db.progress.put(serverProg);
				}
			}

			// --- settings + markdown.new budget + server info + cursor ---
			await updateSyncState({
				settings: response.settings,
				markdownBudget: response.markdown_budget,
				serverInfo: response.server_info,
				cursor: readCursor(response)
			});
		}
	);

	// --- lazy payload fetch for enriched articles with a stale/absent cache ---
	// Run outside the transaction so we don't hold it open during network I/O.
	const enrichedMetas = response.articles.filter((a) => a.status === 'enriched');
	for (const meta of enrichedMetas) {
		const cached = (await db.articles_payload.get(meta.id)) as CachedPayload | undefined;
		if (isPayloadFresh(cached, meta)) continue;

		// Swallow OfflineError mid-pull; the next pull will retry.
		try {
			// Cache-bust by updated_at so a re-enriched article (same
			// enrichment_version) is fetched fresh rather than served from the
			// immutable HTTP cache. Stamp the freshness signal so a later
			// out-of-band re-enrich (other device) is detected by isPayloadFresh.
			const payload = await apiGetArticle(meta.id, undefined, { version: meta.updated_at });
			const stamped: CachedPayload = { ...payload, [PAYLOAD_FRESHNESS_KEY]: meta.updated_at };
			await db.articles_payload.put(stamped);
		} catch (err) {
			if (err instanceof OfflineError) break; // stop trying; no network
			console.warn(`[sync] payload fetch failed for ${meta.id}`, err);
		}
	}
}

// ---------------------------------------------------------------------------
// flushOutbox — local → server (FIFO drain)
// ---------------------------------------------------------------------------

/**
 * A redacted, size-only summary of an outbox entry's payload for telemetry.
 * Never includes raw values — article text, URLs, and settings can be
 * sensitive — only the key set and the serialized byte size.
 */
function payloadSummary(entry: OutboxEntry): Record<string, unknown> {
	let bytes: number;
	try {
		bytes = JSON.stringify(entry.payload ?? null).length;
	} catch {
		bytes = -1;
	}
	const keys =
		entry.payload && typeof entry.payload === 'object'
			? Object.keys(entry.payload as Record<string, unknown>)
			: [];
	return { kind: entry.kind, payload_keys: keys, payload_bytes: bytes };
}

/**
 * Drain the outbox FIFO, sending each entry to the server.
 * - Stops on OfflineError (will retry on next sync).
 * - On 401: keeps the entry, clears the session, and stops.
 * - Drops entries that produce permanent 4xx errors (reports to Sentry first —
 *   this is irrecoverable user-write loss — then logs and deletes).
 * - On 5xx: keeps the entry and stops (reports to Sentry; a persistent 500 can
 *   otherwise wedge the whole outbox silently).
 * - LWW for progress: skip if a newer local progress row exists.
 */
export async function flushOutbox(): Promise<void> {
	// Read all pending entries in insertion order.
	const entries = await db.outbox.orderBy('id').toArray();

	for (const entry of entries) {
		addSyncBreadcrumb('outbox dispatch', { id: entry.id, kind: entry.kind });
		try {
			await dispatchEntry(entry);
			// Success — remove from outbox.
			await db.outbox.delete(entry.id!);
		} catch (err) {
			if (err instanceof OfflineError) return; // stop; retry later

			if (err instanceof ApiError && err.status === 401) {
				// Session lost — keep the entry for after re-login and stop draining.
				await clearSession();
				return;
			}

			if (err instanceof ApiError && err.status >= 400 && err.status < 500) {
				// Permanent client error — drop to avoid infinite retry. This is
				// irrecoverable loss of a queued write, so report it before deleting
				// (with a redacted, size-only payload summary — never the raw body).
				captureError(err, {
					area: 'sync',
					extra: { kind: entry.kind, http_status: err.status, ...payloadSummary(entry) }
				});
				console.error(
					`[sync] dropping outbox entry ${entry.id} (${entry.kind}): ${err.status}`,
					err.body
				);
				await db.outbox.delete(entry.id!);
				continue;
			}

			// Transient server error (5xx) — keep the entry and stop; will retry on
			// the next sync. A persistent 500 wedges the whole outbox, so surface it
			// to Sentry rather than only console.warn.
			if (err instanceof ApiError) {
				captureError(err, {
					area: 'sync',
					extra: { kind: entry.kind, http_status: err.status, ...payloadSummary(entry) }
				});
			}
			console.warn(`[sync] outbox flush stopped at entry ${entry.id} (${entry.kind})`, err);
			return;
		}
	}
}

/** Dispatch a single outbox entry to the correct API call. */
async function dispatchEntry(entry: OutboxEntry): Promise<void> {
	switch (entry.kind) {
		case 'progress': {
			const p = entry.payload as { article_id: string } & ProgressUpdate;
			// LWW: compare parsed instants with the current local progress row.
			const local = await db.progress.get(p.article_id);
			if (local && isNewer(local.updated_at, p.updated_at)) return; // superseded
			await putProgress(p);
			return;
		}
		case 'settings': {
			const patch = entry.payload as SettingsPatch;
			const updated = await patchSettings(patch);
			await updateSyncState({ settings: updated });
			return;
		}
		case 'add_article': {
			const { url } = entry.payload as { url: string };
			await apiAddArticle(url);
			return;
		}
		case 'add_text': {
			const { text, title, url } = entry.payload as {
				text: string;
				title: string;
				url?: string;
			};
			await apiAddArticleText(text, title, url);
			return;
		}
		case 'delete_article': {
			const { id } = entry.payload as { id: string };
			await apiDeleteArticle(id);
			return;
		}
		case 'retry': {
			const { id } = entry.payload as { id: string };
			await apiRetryArticle(id);
			return;
		}
		case 'reenrich': {
			const { id, mode } = entry.payload as { id: string; mode: ReEnrichMode };
			await apiReEnrichArticle(id, mode);
			return;
		}
		case 'pin': {
			const { id, pinned } = entry.payload as { id: string; pinned: boolean };
			await apiPinArticle(id, pinned);
			return;
		}
		default: {
			// Exhaustiveness guard — drop unknown kinds.
			const _exhaustive: never = entry.kind as never;
			console.error('[sync] unknown outbox kind', _exhaustive);
		}
	}
}

// ---------------------------------------------------------------------------
// sync — combined entry point (idempotent)
// ---------------------------------------------------------------------------

// Serialise sync runs. The enqueue* helpers and the sync-status store all call
// sync() directly, so two runs can overlap. Because the cursor is a full-list
// snapshot, pull() reconciles deletions by removing local articles absent from
// the server response. If an earlier pull holding a pre-add snapshot commits
// its transaction *after* a newer pull inserted the just-added article, that
// stale snapshot deletes the new article — the "added article doesn't appear
// until reload" bug. Chaining makes each flush+pull atomic w.r.t. other runs,
// so no pull ever commits against a stale snapshot.
let syncChain: Promise<void> = Promise.resolve();

/** Flush outbox then pull from server. Concurrent calls are serialised. */
export function sync(): Promise<void> {
	syncChain = syncChain.then(runSyncOnce, runSyncOnce);
	return syncChain;
}

async function runSyncOnce(): Promise<void> {
	await flushOutbox();
	await pull();
}

// ---------------------------------------------------------------------------
// Enqueue helpers — write to outbox + optimistic local update + trigger sync
// ---------------------------------------------------------------------------

/** Determine whether the browser currently considers itself online. */
function isOnline(): boolean {
	return typeof navigator === 'undefined' || navigator.onLine;
}

/** Enqueue a progress update and optimistically update local db. */
export async function enqueueProgress(progress: Progress): Promise<void> {
	// Optimistic update. Write on newer-or-equal (parsed instants), so a same-
	// instant update from this device still applies.
	const local = await db.progress.get(progress.article_id);
	if (!local || !isNewer(local.updated_at, progress.updated_at)) {
		await db.progress.put(progress);
	}

	await enqueueOutbox('progress', {
		article_id: progress.article_id,
		position: progress.position,
		is_read: progress.is_read,
		updated_at: progress.updated_at
	});

	if (isOnline()) sync().catch(console.warn);
}

/**
 * Mark an article read/unread and reset the reading position to the start.
 * Marking read clears the position so a re-opened article starts fresh; the
 * library hides the progress bar for read articles regardless.
 */
export async function enqueueSetRead(articleId: string, isRead: boolean): Promise<void> {
	await enqueueProgress({
		article_id: articleId,
		position: 0,
		is_read: isRead,
		updated_at: new Date().toISOString()
	});
}

/** Reset reading progress for an article (position to start, clears read flag). */
export async function enqueueResetProgress(articleId: string): Promise<void> {
	await enqueueProgress({
		article_id: articleId,
		position: 0,
		is_read: false,
		updated_at: new Date().toISOString()
	});
}

/** Enqueue a settings patch and optimistically merge into sync_state.settings. */
export async function enqueueSettings(patch: SettingsPatch): Promise<void> {
	// Optimistic merge.
	const state = await getSyncState();
	if (state.settings) {
		await updateSyncState({
			settings: {
				...state.settings,
				...patch
			}
		});
	}

	await enqueueOutbox('settings', patch);

	if (isOnline()) sync().catch(console.warn);
}

/** Enqueue adding an article by URL. */
export async function enqueueAddArticle(url: string): Promise<void> {
	await enqueueOutbox('add_article', { url });
	if (isOnline()) sync().catch(console.warn);
}

/** Enqueue adding an article from pasted raw text (title and source URL optional). */
export async function enqueueAddArticleText(text: string, title = '', url = ''): Promise<void> {
	await enqueueOutbox('add_text', { text, title, url });
	if (isOnline()) sync().catch(console.warn);
}

/** Enqueue deleting an article and optimistically remove it locally. */
export async function enqueueDelete(id: string): Promise<void> {
	// Optimistic delete.
	await db.articles_meta.delete(id);
	await db.articles_payload.delete(id);
	await db.progress.delete(id);

	await enqueueOutbox('delete_article', { id });

	if (isOnline()) sync().catch(console.warn);
}

/**
 * Enqueue a stage-aware retry and optimistically reset the local status to the
 * queue state for the failed stage: enrich_failed re-enriches from `fetched`
 * (content is kept), everything else re-fetches from `queued`. The authoritative
 * status is reconciled on the next sync.
 */
export async function enqueueRetry(id: string): Promise<void> {
	// Optimistic status update.
	const meta = await db.articles_meta.get(id);
	if (meta) {
		const status = meta.status === 'enrich_failed' ? 'fetched' : 'queued';
		await db.articles_meta.put({ ...meta, status });
	}

	await enqueueOutbox('retry', { id });

	if (isOnline()) sync().catch(console.warn);
}

/**
 * Enqueue a user-triggered re-enrichment and optimistically reflect the
 * processing state. `full` re-translates the whole article (status → fetched),
 * `topup` fills only the uncovered spans (status → topup_queued / enriching).
 *
 * The cached payload is deleted so the reader re-fetches the fresh translation
 * once it completes: the server keeps the same enrichment_version on a re-run,
 * so the version-based cache check in pull() would otherwise keep the stale
 * payload. The authoritative status is reconciled on the next sync.
 */
export async function enqueueReEnrich(id: string, mode: ReEnrichMode): Promise<void> {
	// Optimistic status update + cache-bust so the stale translation is not shown.
	const meta = await db.articles_meta.get(id);
	if (meta) {
		const status = mode === 'topup' ? 'enriching' : 'fetched';
		await db.articles_meta.put({ ...meta, status });
	}
	await db.articles_payload.delete(id);

	await enqueueOutbox('reenrich', { id, mode });

	if (isOnline()) sync().catch(console.warn);
}

/**
 * Enqueue a pin/unpin toggle and optimistically update the local article meta.
 * The server stamps updated_at on the change, so the authoritative pinned state
 * is reconciled on the next pull. flushOutbox runs before pull in each sync(),
 * so the optimistic value is confirmed rather than overwritten in the online case.
 */
export async function enqueuePin(id: string, pinned: boolean): Promise<void> {
	// Optimistic update.
	const meta = await db.articles_meta.get(id);
	if (meta) {
		await db.articles_meta.put({ ...meta, pinned });
	}

	await enqueueOutbox('pin', { id, pinned });

	if (isOnline()) sync().catch(console.warn);
}
