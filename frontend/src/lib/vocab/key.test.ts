import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { buildEntryKey, lemmaOf, normalize, phraseEntryKey, wordEntryKey } from './key';
import type { Token } from '$lib/types';

/**
 * The golden table lives with the Go implementation and is read by both sides
 * (internal/vocab/key_test.go reads the same file). That is the whole point: if
 * either normalization drifts, one of the two suites goes red.
 */
const goldenPath = fileURLToPath(
	new URL('../../../../internal/vocab/testdata/normalize_golden.json', import.meta.url)
);
const golden = JSON.parse(readFileSync(goldenPath, 'utf8')) as {
	normalize: { in: string; out: string }[];
	keys: { kind: 'word' | 'phrase'; lang: string; base: string; out: string }[];
};

const token = (text: string, index = 0, lemma?: string): Token => ({
	index,
	text,
	start: 0,
	end: text.length,
	...(lemma === undefined ? {} : { lemma })
});

describe('normalize', () => {
	it('matches the golden table shared with the Go implementation', () => {
		expect(golden.normalize.length).toBeGreaterThan(0);
		for (const c of golden.normalize) {
			expect(normalize(c.in), `normalize(${JSON.stringify(c.in)})`).toBe(c.out);
		}
	});
});

describe('buildEntryKey', () => {
	it('matches the golden table shared with the Go implementation', () => {
		expect(golden.keys.length).toBeGreaterThan(0);
		for (const c of golden.keys) {
			expect(buildEntryKey(c.kind, c.lang, c.base)).toBe(c.out);
		}
	});
});

describe('lemmaOf', () => {
	it('falls back to the lowercased text when the lemma is omitted', () => {
		// The omitempty encoding makes the absent case the common one.
		expect(lemmaOf(token('Home'))).toBe('home');
		expect(lemmaOf({ ...token('Home'), lemma: '' })).toBe('home');
	});

	it('uses the lemma when present', () => {
		expect(lemmaOf(token('children', 0, 'child'))).toBe('child');
	});
});

describe('wordEntryKey', () => {
	it('keys on the lemma so any inflection lands on one entry', () => {
		expect(wordEntryKey('ru', token('Children', 0, 'child'))).toBe('word:ru:child');
		expect(wordEntryKey('ru', token('child'))).toBe('word:ru:child');
	});
});

describe('phraseEntryKey', () => {
	const tokens = [
		token('He', 0),
		token('spilled', 1, 'spill'),
		token('the', 2),
		token('beans', 3, 'bean'),
		token('again', 4)
	];

	it('joins the lemmas of the range', () => {
		expect(phraseEntryKey('ru', tokens, 1, 3)).toBe('phrase:ru:spill the bean');
	});

	it('folds an inflected variant onto the same entry', () => {
		const inflected = [token('spilling', 0, 'spill'), token('the', 1), token('beans', 2, 'bean')];
		expect(phraseEntryKey('ru', inflected, 0, 2)).toBe(phraseEntryKey('ru', tokens, 1, 3));
	});

	it('returns an empty key for a bad range', () => {
		expect(phraseEntryKey('ru', tokens, -1, 2)).toBe('');
		expect(phraseEntryKey('ru', tokens, 3, 1)).toBe('');
		expect(phraseEntryKey('ru', tokens, 0, 99)).toBe('');
	});
});
