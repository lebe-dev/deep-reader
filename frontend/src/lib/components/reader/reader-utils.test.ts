import { describe, it, expect, vi } from 'vitest';
import {
	buildDifficultWordMap,
	buildPhraseMap,
	buildRenderSegments,
	sliceText,
	findCoveringSentence,
	findCoveringSentenceForRange,
	findGlossaryItem,
	resolveClickContent,
	debounce
} from './reader-utils';
import type { Enrichment, GlossaryItem, Sentence, Token } from '$lib/types';

// Word tokenizer mirroring the backend contract closely enough for the segment
// builder: word = run of letters/digits, every other character is a split
// boundary. Byte offsets are UTF-8 (Go semantics) so spans map correctly.
function wordTokens(text: string): Token[] {
	const enc = new TextEncoder();
	const tokens: Token[] = [];
	let index = 0;
	let byte = 0;
	let startByte = -1;
	let buf = '';
	const isWord = (ch: string) => /[\p{L}\p{N}]/u.test(ch);
	const flush = () => {
		if (startByte >= 0) {
			tokens.push({ index: index++, text: buf, start: startByte, end: byte });
			startByte = -1;
			buf = '';
		}
	};
	for (const ch of text) {
		if (isWord(ch)) {
			if (startByte < 0) startByte = byte;
			buf += ch;
		} else {
			flush();
		}
		byte += enc.encode(ch).length;
	}
	flush();
	return tokens;
}

// Build tokens for a string by splitting on spaces, with correct UTF-8 byte
// offsets so sliceText can be exercised the way the real reader uses it.
function tokenize(text: string): Token[] {
	const encoder = new TextEncoder();
	const tokens: Token[] = [];
	let index = 0;
	let cursor = 0; // byte cursor
	for (const word of text.split(' ')) {
		const start = cursor;
		const end = start + encoder.encode(word).length;
		tokens.push({ index, text: word, start, end });
		cursor = end + encoder.encode(' ').length; // account for the space byte
		index++;
	}
	return tokens;
}

const emptyEnrichment: Enrichment = {
	difficult_words: [],
	phrases: [],
	sentences: [],
	glossary: []
};

describe('buildDifficultWordMap', () => {
	it('indexes difficult words by token_index', () => {
		const map = buildDifficultWordMap({
			...emptyEnrichment,
			difficult_words: [
				{ token_index: 2, lemma: 'run', translation: 'бежать', cefr_level: 'B1' },
				{ token_index: 5, lemma: 'quick', translation: 'быстрый', cefr_level: 'A2' }
			]
		});
		expect(map.get(2)?.lemma).toBe('run');
		expect(map.get(5)?.translation).toBe('быстрый');
		expect(map.has(0)).toBe(false);
	});
});

describe('buildPhraseMap', () => {
	it('maps every token index in a phrase range to that phrase', () => {
		const map = buildPhraseMap({
			...emptyEnrichment,
			phrases: [
				{ start_index: 3, end_index: 5, type: 'idiom', text: 'phrase', translation: 'идиома' }
			]
		});
		expect(map.get(3)?.translation).toBe('идиома');
		expect(map.get(4)?.translation).toBe('идиома');
		expect(map.get(5)?.translation).toBe('идиома');
		expect(map.has(2)).toBe(false);
		expect(map.has(6)).toBe(false);
	});

	it('keeps the first phrase when ranges overlap', () => {
		const map = buildPhraseMap({
			...emptyEnrichment,
			phrases: [
				{ start_index: 0, end_index: 2, type: 'idiom', text: 'first phrase', translation: 'first' },
				{ start_index: 2, end_index: 4, type: 'term', text: 'second phrase', translation: 'second' }
			]
		});
		expect(map.get(2)?.translation).toBe('first');
	});
});

describe('sliceText', () => {
	it('reconstructs original text across a token range, preserving spacing', () => {
		const text = 'the quick brown fox';
		const tokens = tokenize(text);
		expect(sliceText(tokens, 1, 2, text)).toBe('quick brown');
	});

	it('uses byte offsets so non-ASCII text before the range does not shift the slice', () => {
		// The em dash — is 3 UTF-8 bytes but 1 JS char; byte-correct slicing is required.
		const text = 'café — naïve word';
		const tokens = tokenize(text);
		// Last token regardless of the multibyte chars before it.
		expect(sliceText(tokens, 3, 3, text)).toBe('word');
	});

	it('returns empty string for out-of-range indices or empty tokens', () => {
		const text = 'one two';
		const tokens = tokenize(text);
		expect(sliceText(tokens, 0, 9, text)).toBe('');
		expect(sliceText([], 0, 0, text)).toBe('');
	});
});

describe('findCoveringSentence', () => {
	const sentences: Sentence[] = [
		{ start_index: 0, end_index: 10, translation: 'wide' },
		{ start_index: 3, end_index: 6, translation: 'tight' }
	];

	it('returns the tightest sentence containing the index', () => {
		expect(findCoveringSentence(5, sentences)?.translation).toBe('tight');
	});

	it('falls back to the only covering sentence when no tighter one exists', () => {
		expect(findCoveringSentence(1, sentences)?.translation).toBe('wide');
	});

	it('returns undefined when no sentence covers the index', () => {
		expect(findCoveringSentence(99, sentences)).toBeUndefined();
	});
});

describe('findCoveringSentenceForRange', () => {
	const sentences: Sentence[] = [
		{ start_index: 0, end_index: 10, translation: 'wide' },
		{ start_index: 2, end_index: 5, translation: 'tight' }
	];

	it('returns the tightest sentence fully spanning the selection', () => {
		expect(findCoveringSentenceForRange(2, 5, sentences)?.translation).toBe('tight');
	});

	it('only matches a sentence that contains the whole range', () => {
		// Range [4,8] is not contained by the tight [2,5] sentence.
		expect(findCoveringSentenceForRange(4, 8, sentences)?.translation).toBe('wide');
	});

	it('returns undefined when no sentence spans the range', () => {
		expect(findCoveringSentenceForRange(5, 20, sentences)).toBeUndefined();
	});
});

describe('findGlossaryItem', () => {
	const glossary: GlossaryItem[] = [
		{ term: 'API', definition: 'application programming interface' },
		{ term: 'token', definition: 'a unit of text' }
	];

	it('matches case-insensitively on a substring of the phrase', () => {
		expect(findGlossaryItem('the public api surface', glossary)?.term).toBe('API');
	});

	it('returns undefined when no term appears in the phrase', () => {
		expect(findGlossaryItem('nothing relevant here', glossary)).toBeUndefined();
	});
});

describe('resolveClickContent', () => {
	const text = 'the quick brown fox jumps';
	const tokens = tokenize(text);
	const difficultWordMap = buildDifficultWordMap({
		...emptyEnrichment,
		difficult_words: [{ token_index: 4, lemma: 'jump', translation: 'прыгать', cefr_level: 'B1' }]
	});
	const phraseMap = buildPhraseMap({
		...emptyEnrichment,
		phrases: [{ start_index: 1, end_index: 2, type: 'term', text: 'phrase', translation: 'фраза' }]
	});

	it('prefers a phrase over a single difficult word at the same index', () => {
		// Token 4 is a difficult word; add an overlapping phrase to assert priority.
		const overlapping = buildPhraseMap({
			...emptyEnrichment,
			phrases: [
				{
					start_index: 4,
					end_index: 4,
					type: 'idiom',
					text: 'phrase',
					translation: 'фраза-приоритет'
				}
			]
		});
		const content = resolveClickContent(4, tokens, text, difficultWordMap, overlapping);
		expect(content?.kind).toBe('phrase');
	});

	it('resolves a phrase with its reconstructed original text and range', () => {
		const content = resolveClickContent(1, tokens, text, difficultWordMap, phraseMap);
		expect(content).toMatchObject({
			kind: 'phrase',
			original: 'quick brown',
			translationOrDefinition: 'фраза',
			startIndex: 1,
			endIndex: 2
		});
	});

	it('resolves a single difficult word when no phrase covers the index', () => {
		const content = resolveClickContent(4, tokens, text, difficultWordMap, phraseMap);
		expect(content).toMatchObject({
			kind: 'word',
			original: 'jumps',
			translation: 'прыгать',
			lemma: 'jump',
			cefrLevel: 'B1'
		});
	});

	it('returns null for a plain token with no enrichment', () => {
		expect(resolveClickContent(0, tokens, text, difficultWordMap, phraseMap)).toBeNull();
	});
});

describe('debounce', () => {
	it('invokes the function once after the delay, with the latest args', () => {
		vi.useFakeTimers();
		const spy = vi.fn();
		const debounced = debounce(spy, 100);

		debounced('a');
		debounced('b');
		expect(spy).not.toHaveBeenCalled();

		vi.advanceTimersByTime(100);
		expect(spy).toHaveBeenCalledTimes(1);
		expect(spy).toHaveBeenCalledWith('b');

		vi.useRealTimers();
	});
});

describe('buildRenderSegments', () => {
	it('renders prose with no markdown as words and gaps', () => {
		const text = 'Hello brave world';
		const segs = buildRenderSegments(wordTokens(text), text);
		expect(segs.map((s) => s.kind)).toEqual(['word', 'gap', 'word', 'gap', 'word']);
		const words = segs.filter((s) => s.kind === 'word');
		expect(words.map((w) => (w.kind === 'word' ? w.text : ''))).toEqual([
			'Hello',
			'brave',
			'world'
		]);
	});

	it('turns a markdown image into a single image segment', () => {
		const text = 'See ![Cat photo](https://ex.com/cat.png) here.';
		const segs = buildRenderSegments(wordTokens(text), text);
		const image = segs.find((s) => s.kind === 'image');
		expect(image).toEqual({ kind: 'image', alt: 'Cat photo', url: 'https://ex.com/cat.png' });
		// Alt/url fragments must not leak as word tokens.
		const wordText = segs.flatMap((s) => (s.kind === 'word' ? [s.text] : []));
		expect(wordText).toContain('See');
		expect(wordText).toContain('here');
		expect(wordText).not.toContain('Cat');
		expect(wordText).not.toContain('png');
	});

	it('turns a markdown link into a single link segment', () => {
		const text = 'Read [the blog](https://ex.com/post) now.';
		const segs = buildRenderSegments(wordTokens(text), text);
		const link = segs.find((s) => s.kind === 'link');
		expect(link).toEqual({ kind: 'link', text: 'the blog', url: 'https://ex.com/post' });
		const wordText = segs.flatMap((s) => (s.kind === 'word' ? [s.text] : []));
		expect(wordText).not.toContain('blog');
	});

	it('maps byte offsets correctly when multibyte text precedes a span', () => {
		const text = '«crazy» ![x](https://ex.com/a.png)';
		const segs = buildRenderSegments(wordTokens(text), text);
		const image = segs.find((s) => s.kind === 'image');
		expect(image).toEqual({ kind: 'image', alt: 'x', url: 'https://ex.com/a.png' });
		const wordText = segs.flatMap((s) => (s.kind === 'word' ? [s.text] : []));
		expect(wordText).toContain('crazy');
	});

	it('leaves non-http(s) image refs as raw prose', () => {
		const text = '![x](javascript:alert(1))';
		const segs = buildRenderSegments(wordTokens(text), text);
		expect(segs.some((s) => s.kind === 'image')).toBe(false);
		const wordText = segs.flatMap((s) => (s.kind === 'word' ? [s.text] : []));
		expect(wordText).toContain('javascript');
	});

	it('supports an empty image alt', () => {
		const text = '![](https://ex.com/a.png)';
		const segs = buildRenderSegments(wordTokens(text), text);
		expect(segs.find((s) => s.kind === 'image')).toEqual({
			kind: 'image',
			alt: '',
			url: 'https://ex.com/a.png'
		});
	});

	it('preserves the original token index of words after a span', () => {
		const text = 'a ![x](https://e.com/p.png) b';
		const tokens = wordTokens(text);
		const lastToken = tokens[tokens.length - 1];
		expect(lastToken.text).toBe('b');
		const segs = buildRenderSegments(tokens, text);
		const bSeg = segs.find((s) => s.kind === 'word' && s.text === 'b');
		expect(bSeg?.kind === 'word' && bSeg.index).toBe(lastToken.index);
	});

	it('returns an empty array for no tokens', () => {
		expect(buildRenderSegments([], '')).toEqual([]);
	});
});
