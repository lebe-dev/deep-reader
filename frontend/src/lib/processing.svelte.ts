// Reactive pipeline-activity store (Svelte 5 runes): how many articles are
// currently being fetched or enriched.
//
// It exists so ambient UI — the app mark's bubbles — can react to the backend
// working, without those components knowing anything about the article schema.
//
// Named exports only; no default export.

import { browser } from '$app/environment';
import { liveQuery } from 'dexie';
import { db } from '$lib/db';
import { captureError } from '$lib/sentry';
import type { Status } from '$lib/types';

/**
 * The statuses that mean "the pipeline is working on this". The in-flight states
 * are included alongside the queued ones because an article picked up by a
 * worker is exactly what "busy" should mean; the terminal and failed states are
 * not.
 */
export const PROCESSING_STATUSES: Status[] = [
	'queued',
	'fetching',
	'fetched',
	'enriching',
	'topup_queued'
];

interface ProcessingState {
	/** Articles currently in a processing status. */
	count: number;
}

export const processing = $state<ProcessingState>({ count: 0 });

/** True when at least one article is being processed. */
export function isProcessingActive(): boolean {
	return processing.count > 0;
}

/**
 * Watch the local library for in-flight work, returning the stop function.
 *
 * The query counts against the `status` index rather than loading rows: this
 * runs for the whole session in the layout, and the mark only needs a number.
 * A query failure leaves the count at 0 — ambient decoration must never break
 * the shell it lives in.
 */
export function watchProcessing(): () => void {
	if (!browser) return () => {};

	const subscription = liveQuery(() =>
		db.articles_meta.where('status').anyOf(PROCESSING_STATUSES).count()
	).subscribe({
		next(count) {
			processing.count = count;
		},
		error(err) {
			captureError(err, { area: 'ui', extra: { query: 'processing_count' } });
			processing.count = 0;
		}
	});

	return () => subscription.unsubscribe();
}
