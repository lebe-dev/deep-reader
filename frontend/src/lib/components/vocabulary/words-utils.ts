// Pure helpers for the /words screen: search, filter and sort over the
// collected vocabulary. Framework-agnostic so they can be unit-tested without
// IndexedDB or a DOM (see words-utils.test.ts), mirroring library-utils.ts.
//
// Named exports only; no default export.

import type { LookupKind, VocabEntry } from '$lib/types';

/** Which entries the list shows. */
export type WordsFilter = 'all' | 'word' | 'phrase';

/** How the list is ordered. */
export type WordsSort = 'frequency' | 'recent' | 'oldest' | 'alpha';

/** The default ordering: most-looked-up first, which is the useful signal. */
export const DEFAULT_SORT: WordsSort = 'frequency';

const FILTERS: WordsFilter[] = ['all', 'word', 'phrase'];
const SORTS: WordsSort[] = ['frequency', 'recent', 'oldest', 'alpha'];

/** Coerce an arbitrary URL query value to a valid filter. */
export function parseFilter(value: string | null | undefined): WordsFilter {
	return FILTERS.includes(value as WordsFilter) ? (value as WordsFilter) : 'all';
}

/** Coerce an arbitrary URL query value to a valid sort. */
export function parseSort(value: string | null | undefined): WordsSort {
	return SORTS.includes(value as WordsSort) ? (value as WordsSort) : DEFAULT_SORT;
}

/**
 * Whether an entry matches the search query. The query is matched against the
 * lemma, the translation AND the observed surface forms, so typing an
 * inflection ("resiliency") finds the entry it folded onto ("resilient").
 */
export function matchesQuery(entry: VocabEntry, query: string): boolean {
	const q = query.trim().toLowerCase();
	if (q === '') return true;
	if (entry.lemma.toLowerCase().includes(q)) return true;
	if (entry.latest_translation.toLowerCase().includes(q)) return true;
	return (entry.surface_forms ?? []).some((form) => form.toLowerCase().includes(q));
}

/** Whether an entry passes the kind filter. */
export function matchesFilter(entry: VocabEntry, filter: WordsFilter): boolean {
	return filter === 'all' || entry.kind === (filter as LookupKind);
}

/**
 * Comparator for the requested ordering. `countOf` supplies the DISPLAYED count
 * (server count plus un-flushed taps), so the list order matches the numbers
 * the user can see.
 *
 * Ties break on the entry key so the order is stable across renders — a list
 * that reshuffles on every keystroke is worse than a slightly arbitrary one.
 */
export function compareWords(
	a: VocabEntry,
	b: VocabEntry,
	sort: WordsSort,
	countOf: (entry: VocabEntry) => number
): number {
	switch (sort) {
		case 'frequency': {
			const diff = countOf(b) - countOf(a);
			if (diff !== 0) return diff;
			break;
		}
		case 'recent': {
			const diff = b.last_seen.localeCompare(a.last_seen);
			if (diff !== 0) return diff;
			break;
		}
		case 'oldest': {
			const diff = a.first_seen.localeCompare(b.first_seen);
			if (diff !== 0) return diff;
			break;
		}
		case 'alpha': {
			const diff = a.lemma.localeCompare(b.lemma, 'en');
			if (diff !== 0) return diff;
			break;
		}
	}
	return a.entry_key.localeCompare(b.entry_key);
}

/** Options for {@link selectWords}. */
export interface SelectWordsOptions {
	query?: string;
	filter?: WordsFilter;
	sort?: WordsSort;
	/** Displayed count per entry; defaults to the stored server count. */
	countOf?: (entry: VocabEntry) => number;
}

/**
 * The list the /words screen renders: tombstoned entries removed, search and
 * filter applied, then ordered. Returns a new array; the input is untouched.
 */
export function selectWords(entries: VocabEntry[], options: SelectWordsOptions = {}): VocabEntry[] {
	const { query = '', filter = 'all', sort = DEFAULT_SORT } = options;
	const countOf = options.countOf ?? ((entry: VocabEntry) => entry.count);

	return entries
		.filter((entry) => !entry.deleted_at)
		.filter((entry) => matchesFilter(entry, filter))
		.filter((entry) => matchesQuery(entry, query))
		.sort((a, b) => compareWords(a, b, sort, countOf));
}

/**
 * The observed inflections worth showing under an entry. Returns an empty array
 * when the forms add nothing beyond the lemma itself — "seen as: resilient" for
 * an entry whose lemma is already "resilient" is noise.
 */
export function displayableSurfaceForms(entry: VocabEntry): string[] {
	const lemma = entry.lemma.toLowerCase();
	const forms = (entry.surface_forms ?? []).filter((form) => form !== '');
	if (forms.length === 0) return [];
	if (forms.length === 1 && forms[0].toLowerCase() === lemma) return [];
	return forms;
}

/** Split `text` into the segments matching `query` and those around them. */
export interface HighlightSegment {
	text: string;
	match: boolean;
}

/**
 * Split text for search-match highlighting. Case-insensitive; an empty or
 * unmatched query yields one non-matching segment, so callers can render the
 * result uniformly.
 */
export function highlightSegments(text: string, query: string): HighlightSegment[] {
	const q = query.trim().toLowerCase();
	if (q === '') return [{ text, match: false }];

	const segments: HighlightSegment[] = [];
	const lower = text.toLowerCase();
	let cursor = 0;

	for (;;) {
		const at = lower.indexOf(q, cursor);
		if (at === -1) break;
		if (at > cursor) segments.push({ text: text.slice(cursor, at), match: false });
		segments.push({ text: text.slice(at, at + q.length), match: true });
		cursor = at + q.length;
	}
	if (cursor < text.length) segments.push({ text: text.slice(cursor), match: false });
	return segments.length > 0 ? segments : [{ text, match: false }];
}
