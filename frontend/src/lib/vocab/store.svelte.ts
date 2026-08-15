// Reactive vocabulary store (Svelte 5 runes) — the in-memory snapshot the
// reader overlay and the /words screen read (WORD-CACHE-ARCH.md §10.2).
//
// It is reactive on purpose: tapping a word makes its other occurrences in the
// current article light up immediately, which is the clearest feedback that the
// word was collected.
//
// Named exports only; no default export.

import { browser } from '$app/environment';
import { db } from '$lib/db';
import { captureError } from '$lib/sentry';
import type { VocabEntry } from '$lib/types';
import { buildVocabIndex, emptyVocabIndex, type VocabIndex } from './overlay';
import { onVocabChanged } from './changes';

export interface VocabStoreState {
	/** Live (non-tombstoned) entries, as last read from Dexie. */
	entries: VocabEntry[];
	/**
	 * Pending un-flushed taps per entry key. The displayed count is
	 * `server count + pending`, which cannot drift: once the outbox drains the
	 * map empties and the next pull carries the authoritative count (§6.2).
	 */
	pending: Map<string, number>;
	/** True once the first load from Dexie has completed. */
	loaded: boolean;
}

export const vocabStore = $state<VocabStoreState>({
	entries: [],
	pending: new Map(),
	loaded: false
});

/**
 * The matcher index for the reader overlay, rebuilt whenever the entries
 * change. Callers that need it per-article should still memoise their own
 * `$derived` over `(article, index)`.
 */
let cachedIndex: VocabIndex = emptyVocabIndex();
let cachedFor: VocabEntry[] | null = null;

export function vocabIndex(): VocabIndex {
	if (cachedFor !== vocabStore.entries) {
		cachedIndex = buildVocabIndex(vocabStore.entries);
		cachedFor = vocabStore.entries;
	}
	return cachedIndex;
}

/**
 * Reload the snapshot from Dexie. Called after a pull, after each capture, and
 * on reader/`/words` mount. Failures are reported but never thrown: a missing
 * overlay is a degraded reader, not a broken one.
 */
export async function refreshVocab(): Promise<void> {
	if (!browser) return;
	try {
		const [rows, outbox] = await Promise.all([
			db.vocab_entries.toArray(),
			// Both kinds are un-flushed occurrences of an entry: a passive tap and a
			// deliberate save count the same towards the displayed number.
			db.outbox.where('kind').anyOf('lookup', 'manual_lookup').toArray()
		]);

		const pending = new Map<string, number>();
		for (const entry of outbox) {
			const key = (entry.payload as { entry_key?: string }).entry_key;
			if (!key) continue;
			pending.set(key, (pending.get(key) ?? 0) + 1);
		}

		vocabStore.entries = rows.filter((row) => !row.deleted_at);
		vocabStore.pending = pending;
		vocabStore.loaded = true;
	} catch (err) {
		captureError(err, { area: 'vocab', extra: { op: 'refreshVocab' } });
	}
}

// Reload whenever anything writes vocabulary rows to Dexie — the sync engine
// applying a pull delta, or the outbox filling in the translation of a word the
// user just saved (WORD-CACHE-ARCH.md §18.3). Registered once, at module load,
// because this snapshot has no owner component: the reader and /words both read
// it, and neither is guaranteed to be mounted when the write lands. Without it
// the snapshot only reloaded on mount, so a saved word kept showing
// "Translating…" until the reader was reopened.
if (browser) onVocabChanged(() => void refreshVocab());

/**
 * The count to display for an entry: the authoritative server count plus taps
 * still sitting in the outbox.
 */
export function displayedCount(entry: VocabEntry): number {
	return entry.count + (vocabStore.pending.get(entry.entry_key) ?? 0);
}

/** Whether an entry's displayed count includes un-flushed taps. */
export function hasPendingLookups(entry: VocabEntry): boolean {
	return (vocabStore.pending.get(entry.entry_key) ?? 0) > 0;
}

/**
 * Whether the entry is still waiting for its translation.
 *
 * Only a manually saved term can be in this state: it is recorded the moment
 * the user asks for it — offline included — and the translation arrives later,
 * when the outbox reaches `POST /api/translate` (WORD-CACHE-ARCH.md §18). The
 * UI shows the word with a "translating" hint rather than an empty line.
 */
export function isTranslationPending(entry: VocabEntry): boolean {
	return entry.latest_translation.trim() === '';
}

/**
 * Remove an entry locally (optimistic) and queue the deletion for the backend.
 * The removal is soft server-side and the entry revives on the next lookup, so
 * a misclick costs one tap in an article — which is why the UI needs no
 * confirmation dialog (§13.4).
 */
export async function removeVocabEntry(entryKey: string): Promise<void> {
	const { enqueueOutbox } = await import('$lib/db');
	await db.vocab_entries.delete(entryKey);
	await enqueueOutbox('vocab_delete', { entry_key: entryKey });
	await refreshVocab();
}
