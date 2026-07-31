import { describe, expect, it } from 'vitest';

import {
	compareWords,
	displayableSurfaceForms,
	highlightSegments,
	matchesFilter,
	matchesQuery,
	parseFilter,
	parseSort,
	selectWords
} from './words-utils';
import type { VocabEntry } from '$lib/types';

const entry = (over: Partial<VocabEntry> = {}): VocabEntry => ({
	entry_key: 'word:ru:resilient',
	kind: 'word',
	lemma: 'resilient',
	target_lang: 'ru',
	surface_forms: ['resilient'],
	count: 3,
	first_seen: '2026-06-01T00:00:00Z',
	last_seen: '2026-06-10T00:00:00Z',
	latest_translation: 'устойчивый',
	latest_context: '',
	latest_article_id: 'a1',
	latest_article_title: 'The Economist',
	updated_at: '2026-06-10T00:00:00Z',
	...over
});

describe('parseFilter / parseSort', () => {
	it('accepts the valid values', () => {
		expect(parseFilter('word')).toBe('word');
		expect(parseFilter('phrase')).toBe('phrase');
		expect(parseSort('recent')).toBe('recent');
		expect(parseSort('alpha')).toBe('alpha');
	});

	it('falls back to the defaults for anything else', () => {
		// The values come from the URL, so garbage must not break the page.
		expect(parseFilter('nonsense')).toBe('all');
		expect(parseFilter(null)).toBe('all');
		expect(parseSort(undefined)).toBe('frequency');
	});
});

describe('matchesQuery', () => {
	it('matches the lemma and the translation', () => {
		expect(matchesQuery(entry(), 'resil')).toBe(true);
		expect(matchesQuery(entry(), 'устойч')).toBe(true);
	});

	it('matches an observed inflection, so typing a surface form finds its entry', () => {
		expect(matchesQuery(entry({ surface_forms: ['resilient', 'resiliency'] }), 'resiliency')).toBe(
			true
		);
	});

	it('is case-insensitive and treats an empty query as "everything"', () => {
		expect(matchesQuery(entry(), 'RESIL')).toBe(true);
		expect(matchesQuery(entry(), '   ')).toBe(true);
	});

	it('rejects a non-match', () => {
		expect(matchesQuery(entry(), 'zzz')).toBe(false);
	});
});

describe('matchesFilter', () => {
	it('filters by kind', () => {
		expect(matchesFilter(entry(), 'all')).toBe(true);
		expect(matchesFilter(entry(), 'word')).toBe(true);
		expect(matchesFilter(entry(), 'phrase')).toBe(false);
		expect(matchesFilter(entry({ kind: 'phrase' }), 'phrase')).toBe(true);
	});
});

describe('compareWords', () => {
	const countOf = (e: VocabEntry) => e.count;

	it('orders by count descending for "frequency"', () => {
		const a = entry({ entry_key: 'a', count: 2 });
		const b = entry({ entry_key: 'b', count: 9 });
		expect(compareWords(a, b, 'frequency', countOf)).toBeGreaterThan(0);
	});

	it('orders by last_seen descending for "recent"', () => {
		const older = entry({ entry_key: 'a', last_seen: '2026-06-01T00:00:00Z' });
		const newer = entry({ entry_key: 'b', last_seen: '2026-06-20T00:00:00Z' });
		expect(compareWords(older, newer, 'recent', countOf)).toBeGreaterThan(0);
	});

	it('orders by first_seen ascending for "oldest"', () => {
		const first = entry({ entry_key: 'a', first_seen: '2026-01-01T00:00:00Z' });
		const later = entry({ entry_key: 'b', first_seen: '2026-06-01T00:00:00Z' });
		expect(compareWords(first, later, 'oldest', countOf)).toBeLessThan(0);
	});

	it('orders alphabetically for "alpha"', () => {
		const a = entry({ entry_key: 'a', lemma: 'apple' });
		const z = entry({ entry_key: 'z', lemma: 'zebra' });
		expect(compareWords(a, z, 'alpha', countOf)).toBeLessThan(0);
	});

	it('breaks ties on the entry key so the order is stable', () => {
		const a = entry({ entry_key: 'word:ru:aaa', count: 5 });
		const b = entry({ entry_key: 'word:ru:bbb', count: 5 });
		expect(compareWords(a, b, 'frequency', countOf)).toBeLessThan(0);
		expect(compareWords(b, a, 'frequency', countOf)).toBeGreaterThan(0);
	});
});

describe('selectWords', () => {
	const entries = [
		entry({ entry_key: 'word:ru:mitigate', lemma: 'mitigate', count: 3 }),
		entry({ entry_key: 'word:ru:ubiquitous', lemma: 'ubiquitous', count: 7 }),
		entry({
			entry_key: 'phrase:ru:take off',
			kind: 'phrase',
			lemma: 'take off',
			count: 4,
			latest_translation: 'взлетать'
		})
	];

	it('orders by frequency by default', () => {
		expect(selectWords(entries).map((e) => e.lemma)).toEqual([
			'ubiquitous',
			'take off',
			'mitigate'
		]);
	});

	it('applies the kind filter', () => {
		expect(selectWords(entries, { filter: 'phrase' }).map((e) => e.lemma)).toEqual(['take off']);
	});

	it('applies the search query', () => {
		expect(selectWords(entries, { query: 'взлет' }).map((e) => e.lemma)).toEqual(['take off']);
	});

	it('excludes tombstoned entries', () => {
		const withTombstone = [...entries, entry({ entry_key: 'gone', deleted_at: '2026-06-11' })];
		expect(selectWords(withTombstone).some((e) => e.entry_key === 'gone')).toBe(false);
	});

	it('orders by the DISPLAYED count, including un-flushed taps', () => {
		// "mitigate" has three pending lookups, so it must outrank "ubiquitous"
		// in the list exactly as it does in the numbers on screen.
		const countOf = (e: VocabEntry) => e.count + (e.lemma === 'mitigate' ? 6 : 0);
		expect(selectWords(entries, { countOf }).map((e) => e.lemma)).toEqual([
			'mitigate',
			'ubiquitous',
			'take off'
		]);
	});

	it('does not mutate the input', () => {
		const input = [...entries];
		selectWords(input, { sort: 'alpha' });
		expect(input.map((e) => e.lemma)).toEqual(entries.map((e) => e.lemma));
	});
});

describe('displayableSurfaceForms', () => {
	it('is empty when the only form is the lemma itself', () => {
		expect(displayableSurfaceForms(entry({ surface_forms: ['resilient'] }))).toEqual([]);
	});

	it('is empty when there are no forms at all', () => {
		expect(displayableSurfaceForms(entry({ surface_forms: [] }))).toEqual([]);
	});

	it('returns the forms when they add something', () => {
		expect(displayableSurfaceForms(entry({ surface_forms: ['resilient', 'resiliency'] }))).toEqual([
			'resilient',
			'resiliency'
		]);
	});
});

describe('highlightSegments', () => {
	it('splits around each match, case-insensitively', () => {
		expect(highlightSegments('Resilient resilience', 'resil')).toEqual([
			{ text: 'Resil', match: true },
			{ text: 'ient ', match: false },
			{ text: 'resil', match: true },
			{ text: 'ience', match: false }
		]);
	});

	it('returns the whole text unmatched for an empty query', () => {
		expect(highlightSegments('resilient', '')).toEqual([{ text: 'resilient', match: false }]);
	});

	it('returns the whole text unmatched when nothing matches', () => {
		expect(highlightSegments('resilient', 'zzz')).toEqual([{ text: 'resilient', match: false }]);
	});

	it('preserves the original text when the segments are rejoined', () => {
		const text = 'The resilient market';
		expect(
			highlightSegments(text, 'resilient')
				.map((s) => s.text)
				.join('')
		).toBe(text);
	});
});
