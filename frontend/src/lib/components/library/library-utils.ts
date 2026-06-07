// Pure helpers for the library view: reading-progress percentage and the
// pinned-first / unread-first ordering. Kept framework-agnostic so they can be
// unit-tested without IndexedDB or a DOM (see library-utils.test.ts).
//
// Named exports only; no default export.

import type { ArticleMeta } from '$lib/types';

/**
 * Reading-progress percentage [0,100] for the furthest-seen token `position`
 * (0-based index, as persisted in Progress.position) within an article of
 * `tokenCount` tokens. Returns 0 when the token count is unknown/zero, and
 * clamps an out-of-range position to the [0,100] band.
 *
 * `position` is the furthest token index the reader has scrolled past, so the
 * count of tokens seen is `position + 1`; at the last token this yields 100%.
 */
export function readingProgressPercent(position: number, tokenCount: number): number {
	if (tokenCount <= 0 || position < 0) return 0;
	const seen = Math.min(position + 1, tokenCount);
	return Math.min(100, Math.max(0, Math.round((seen / tokenCount) * 100)));
}

function isProcessing(status: ArticleMeta['status']): boolean {
	return status === 'queued' || status === 'fetching' || status === 'fetched' || status === 'enriching';
}

/**
 * Comparator ordering the library: in-progress articles first, then pinned,
 * then unread before read. Articles that compare equal keep their incoming
 * relative order (the caller pre-sorts by created_at descending), so newest
 * stays first within each group. `readSet` holds the ids of articles marked read.
 */
export function compareLibrary(a: ArticleMeta, b: ArticleMeta, readSet: Set<string>): number {
	const processingDiff = (isProcessing(b.status) ? 1 : 0) - (isProcessing(a.status) ? 1 : 0);
	if (processingDiff !== 0) return processingDiff;

	const pinnedDiff = (b.pinned ? 1 : 0) - (a.pinned ? 1 : 0);
	if (pinnedDiff !== 0) return pinnedDiff;

	const aRead = readSet.has(a.id) ? 1 : 0;
	const bRead = readSet.has(b.id) ? 1 : 0;
	return aRead - bRead;
}

/**
 * Return a new array ordered for the library view. The input should already be
 * sorted by created_at descending; this applies the pinned-first / unread-first
 * grouping on top with a stable sort.
 */
export function sortLibrary(articles: ArticleMeta[], readSet: Set<string>): ArticleMeta[] {
	return [...articles].sort((a, b) => compareLibrary(a, b, readSet));
}
