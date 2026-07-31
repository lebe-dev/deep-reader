import { describe, expect, it } from 'vitest';

import {
	buildOverlayIndices,
	buildVocabIndex,
	emptyVocabIndex,
	matchPhraseAt,
	matchWord
} from './overlay';
import type { Token, VocabEntry } from '$lib/types';

const token = (index: number, text: string, lemma?: string): Token => ({
	index,
	text,
	start: index * 10,
	end: index * 10 + text.length,
	...(lemma === undefined ? {} : { lemma })
});

const wordEntry = (over: Partial<VocabEntry> = {}): VocabEntry => ({
	entry_key: 'word:ru:resilient',
	kind: 'word',
	lemma: 'resilient',
	target_lang: 'ru',
	surface_forms: ['resilient'],
	count: 3,
	first_seen: '',
	last_seen: '',
	latest_translation: 'устойчивый',
	latest_context: '',
	latest_article_id: '',
	latest_article_title: '',
	updated_at: '',
	...over
});

const phraseEntry = (over: Partial<VocabEntry> = {}): VocabEntry =>
	wordEntry({
		entry_key: 'phrase:ru:spill the bean',
		kind: 'phrase',
		lemma: 'spilled the beans',
		latest_translation: 'выдать секрет',
		latest_phrase_type: 'idiom',
		surface_forms: [],
		...over
	});

describe('matchWord', () => {
	it('matches a collected word in any inflection, via the token lemma', () => {
		const index = buildVocabIndex([wordEntry()]);
		// The article says "resilience"; the vocabulary holds "resilient".
		expect(matchWord(index, token(0, 'resilience', 'resilient'))?.entry_key).toBe(
			'word:ru:resilient'
		);
	});

	it('matches the uninflected form with no lemma on the token', () => {
		const index = buildVocabIndex([wordEntry()]);
		expect(matchWord(index, token(0, 'Resilient'))?.entry_key).toBe('word:ru:resilient');
	});

	it('falls back to observed surface forms for out-of-vocabulary entries', () => {
		// The lemmatizer never recognised this, so the key came from elsewhere.
		const index = buildVocabIndex([
			wordEntry({ entry_key: 'word:ru:k8s', lemma: 'k8s', surface_forms: ['kubernetes'] })
		]);
		expect(matchWord(index, token(0, 'Kubernetes'))?.entry_key).toBe('word:ru:k8s');
	});

	it('does not match an unrelated word', () => {
		const index = buildVocabIndex([wordEntry()]);
		expect(matchWord(index, token(0, 'markets', 'market'))).toBeUndefined();
	});

	it('ignores tombstoned entries so a deleted word leaves the overlay at once', () => {
		const index = buildVocabIndex([wordEntry({ deleted_at: '2026-06-11T00:00:00Z' })]);
		expect(matchWord(index, token(0, 'resilient'))).toBeUndefined();
	});
});

describe('matchPhraseAt', () => {
	const tokens = [
		token(0, 'He'),
		token(1, 'spilling', 'spill'),
		token(2, 'the'),
		token(3, 'beans', 'bean'),
		token(4, 'again')
	];

	it('matches a phrase sequence in any inflection', () => {
		const index = buildVocabIndex([phraseEntry()]);
		const match = matchPhraseAt(index, tokens, 1);
		expect(match).toMatchObject({ startIndex: 1, endIndex: 3 });
		expect(match?.entry.entry_key).toBe('phrase:ru:spill the bean');
	});

	it('matches from any token inside the phrase, not only its first', () => {
		const index = buildVocabIndex([phraseEntry()]);
		expect(matchPhraseAt(index, tokens, 3)).toMatchObject({ startIndex: 1, endIndex: 3 });
	});

	it('prefers the longest candidate', () => {
		const index = buildVocabIndex([
			phraseEntry(),
			phraseEntry({ entry_key: 'phrase:ru:spill the', lemma: 'spill the' })
		]);
		expect(matchPhraseAt(index, tokens, 1)?.entry.entry_key).toBe('phrase:ru:spill the bean');
	});

	it('does not match a partial or broken sequence', () => {
		const index = buildVocabIndex([phraseEntry()]);
		const other = [token(0, 'spilling', 'spill'), token(1, 'coffee')];
		expect(matchPhraseAt(index, other, 0)).toBeUndefined();
	});

	it('returns undefined for an empty index', () => {
		expect(matchPhraseAt(emptyVocabIndex(), tokens, 1)).toBeUndefined();
	});
});

describe('buildOverlayIndices', () => {
	const tokens = [
		token(0, 'The'),
		token(1, 'resilience', 'resilient'),
		token(2, 'of'),
		token(3, 'resilient')
	];

	it('marks every occurrence of a known lemma, in any inflection', () => {
		const index = buildVocabIndex([wordEntry()]);
		const marked = buildOverlayIndices(index, tokens, () => false);
		expect([...marked].sort()).toEqual([1, 3]);
	});

	it('never marks tokens the enrichment already annotated', () => {
		// Enrichment always outranks the overlay; decorating both would mislead.
		const index = buildVocabIndex([wordEntry()]);
		const marked = buildOverlayIndices(index, tokens, (i) => i === 1);
		expect([...marked]).toEqual([3]);
	});

	it('marks every token of a matched phrase', () => {
		const phraseTokens = [
			token(0, 'He'),
			token(1, 'spilled', 'spill'),
			token(2, 'the'),
			token(3, 'beans', 'bean')
		];
		const index = buildVocabIndex([phraseEntry()]);
		const marked = buildOverlayIndices(index, phraseTokens, () => false);
		expect([...marked].sort()).toEqual([1, 2, 3]);
	});

	it('marks nothing for an empty vocabulary', () => {
		expect(buildOverlayIndices(emptyVocabIndex(), tokens, () => false).size).toBe(0);
	});
});
