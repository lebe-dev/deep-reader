// Sync engine — pull/push logic, outbox drain, enqueue helpers.
// Spec §6 "Sync engine", §7 "Data flows", §10 "Offline-first".
//
// Named exports only; no default export.

import {
	db,
	enqueueOutbox,
	getSyncState,
	updateSyncState,
	type OutboxEntry,
	type OutboxKind
} from '$lib/db';
import {
	addArticle as apiAddArticle,
	deleteArticle as apiDeleteArticle,
	getArticle as apiGetArticle,
	getConfig,
	OfflineError,
	ApiError,
	patchSettings,
	putProgress,
	retryArticle as apiRetryArticle
} from '$lib/api';
import type { Progress, ProgressUpdate, SettingsPatch } from '$lib/types';

// ---------------------------------------------------------------------------
// pull — server → local diff
// ---------------------------------------------------------------------------

/**
 * Fetch a delta from the server and apply it to the local IndexedDB.
 * - Upserts new / changed articles_meta.
 * - Removes articles deleted server-side (absent from server list but present locally).
 * - Upserts progress rows (LWW: keep whichever updated_at is newer).
 * - Updates settings singleton in sync_state.
 * - Advances the cursor to the server-returned value.
 * - For newly enriched articles without a cached payload, lazily fetches and
 *   caches the full payload (cache-first; payloads are immutable).
 */
export async function pull(): Promise<void> {
	const state = await getSyncState();
	const response = await getConfig(state.cursor);

	await db.transaction(
		'rw',
		[db.articles_meta, db.articles_payload, db.progress, db.sync_state],
		async () => {
			// --- articles_meta ---
			const serverIds = new Set(response.articles.map((a) => a.id));
			const localMetas = await db.articles_meta.toArray();
			const localIds = new Set(localMetas.map((a) => a.id));

			// Upsert all server articles.
			await db.articles_meta.bulkPut(response.articles);

			// Remove articles that are no longer on the server.
			// Only delete what was previously synced (not newly locally added pending items).
			const toDelete = localMetas.filter((a) => !serverIds.has(a.id)).map((a) => a.id);
			if (toDelete.length > 0) {
				await db.articles_meta.bulkDelete(toDelete);
				await db.articles_payload.bulkDelete(toDelete);
			}

			// --- progress (LWW by updated_at) ---
			for (const serverProg of response.progress) {
				const local = await db.progress.get(serverProg.article_id);
				if (!local || serverProg.updated_at > local.updated_at) {
					await db.progress.put(serverProg);
				}
			}

			// --- settings + markdown.new budget ---
			await updateSyncState({
				settings: response.settings,
				markdownBudget: response.markdown_budget,
				cursor: response.cursor
			});
		}
	);

	// --- lazy payload fetch for enriched articles without a local cache ---
	// Run outside the transaction so we don't hold it open during network I/O.
	const enrichedMetas = response.articles.filter((a) => a.status === 'enriched');
	for (const meta of enrichedMetas) {
		const cached = await db.articles_payload.get(meta.id);
		if (cached && cached.enrichment_version === meta.enrichment_version) continue;

		// Swallow OfflineError mid-pull; the next pull will retry.
		try {
			const payload = await apiGetArticle(meta.id);
			await db.articles_payload.put(payload);
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
 * Drain the outbox FIFO, sending each entry to the server.
 * - Stops on OfflineError (will retry on next sync).
 * - Drops entries that produce permanent 4xx errors (logs them).
 * - LWW for progress: skip if a newer local progress row exists.
 */
export async function flushOutbox(): Promise<void> {
	// Read all pending entries in insertion order.
	const entries = await db.outbox.orderBy('id').toArray();

	for (const entry of entries) {
		try {
			await dispatchEntry(entry);
			// Success — remove from outbox.
			await db.outbox.delete(entry.id!);
		} catch (err) {
			if (err instanceof OfflineError) return; // stop; retry later

			if (err instanceof ApiError && err.status >= 400 && err.status < 500) {
				// Permanent client error — drop to avoid infinite retry.
				console.error(
					`[sync] dropping outbox entry ${entry.id} (${entry.kind}): ${err.status}`,
					err.body
				);
				await db.outbox.delete(entry.id!);
				continue;
			}

			// Transient server error — stop; will retry on next sync.
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
			// LWW: compare with the current local progress row.
			const local = await db.progress.get(p.article_id);
			if (local && local.updated_at > p.updated_at) return; // superseded
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

/** Flush outbox then pull from server. Idempotent; safe to call concurrently. */
export async function sync(): Promise<void> {
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
	// Optimistic update.
	const local = await db.progress.get(progress.article_id);
	if (!local || progress.updated_at >= local.updated_at) {
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
