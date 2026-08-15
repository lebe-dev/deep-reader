/**
 * A one-line pub/sub for "the vocabulary rows in IndexedDB changed".
 *
 * The in-memory snapshot the reader overlay and /words render from
 * (`vocab/store.svelte.ts`) is loaded from Dexie, but the writers are elsewhere:
 * the sync engine applies the pull delta and writes the translation of a
 * manually saved word once it arrives (WORD-CACHE-ARCH.md §18.3). Without a
 * signal between them the snapshot goes stale until the page remounts — which
 * looked like a word stuck on "Translating…" forever.
 *
 * It is a plain module, deliberately: the sync engine must not import a runes
 * (`*.svelte.ts`) module, because the unit tests run without the Svelte plugin
 * and could not compile `$state`. The engine notifies, the store subscribes.
 *
 * Named exports only; no default export.
 */

type VocabListener = () => void;

const listeners = new Set<VocabListener>();

/** Subscribe to vocabulary changes. Returns the unsubscribe function. */
export function onVocabChanged(listener: VocabListener): () => void {
	listeners.add(listener);
	return () => {
		listeners.delete(listener);
	};
}

/**
 * Announce that `vocab_entries` changed on disk. Fire-and-forget: a subscriber
 * that throws must not take down the sync step that reported the change, so
 * failures are logged and the remaining subscribers still run.
 */
export function notifyVocabChanged(): void {
	for (const listener of listeners) {
		try {
			listener();
		} catch (err) {
			console.warn('[vocab] change subscriber failed', err);
		}
	}
}
