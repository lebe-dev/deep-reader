// Unit tests for capture: event construction, lemma precedence, context
// truncation, and the in-memory dedup that keeps a re-tap from queuing a
// redundant outbox row. `$lib/db` and `$lib/sentry` are mocked so the pure
// helpers can be exercised without IndexedDB.

import { describe, it, expect, vi, beforeEach } from 'vitest';
import type { Enrichment, Token, VocabEntry } from '$lib/types';
import type { PopoverContent } from '$lib/components/reader/reader-utils';

const h = vi.hoisted(() => ({
	enqueueOutbox: vi.fn(async () => 1),
	vocabRows: new Map<string, VocabEntry>(),
	captureError: vi.fn()
}));

vi.mock('$lib/db', () => ({
	enqueueOutbox: h.enqueueOutbox,
	db: {
		vocab_entries: {
			get: async (key: string) => h.vocabRows.get(key),
			put: async (row: VocabEntry) => {
				h.vocabRows.set(row.entry_key, row);
			}
		}
	}
}));

vi.mock('$lib/sentry', () => ({ captureError: h.captureError }));

import {
	buildLookupEvent,
	captureLookup,
	contextFor,
	resetCaptureSession,
	truncateOnWordBoundary,
	MAX_CONTEXT_LEN
} from './capture';

const originalText = 'The market proved remarkably resilient. Everyone noticed.';

const tokens: Token[] = [
	{ index: 0, text: 'The', start: 0, end: 3 },
	{ index: 1, text: 'market', start: 4, end: 10 },
	{ index: 2, text: 'proved', start: 11, end: 17, lemma: 'prove' },
	{ index: 3, text: 'remarkably', start: 18, end: 28, lemma: 'remarkable' },
	{ index: 4, text: 'resilient', start: 29, end: 38 },
	{ index: 5, text: 'Everyone', start: 40, end: 48 },
	{ index: 6, text: 'noticed', start: 49, end: 56, lemma: 'notice' }
];

const enrichment: Enrichment = {
	difficult_words: [],
	phrases: [],
	sentences: [
		{ start_index: 0, end_index: 4, translation: 'Рынок оказался устойчивым.' },
		{ start_index: 5, end_index: 6, translation: 'Все заметили.' }
	],
	glossary: []
};

const wordContent: PopoverContent = {
	kind: 'word',
	tokenIndex: 4,
	original: 'resilient',
	translation: 'устойчивый',
	lemma: 'resilient',
	cefrLevel: 'B2',
	fromGlossary: false
};

const phraseContent: PopoverContent = {
	kind: 'phrase',
	original: 'proved remarkably',
	phraseType: 'idiom',
	translationOrDefinition: 'оказался удивительно',
	startIndex: 2,
	endIndex: 3
};

function input(content: PopoverContent) {
	return {
		articleId: 'a1',
		articleTitle: 'The Economist',
		content,
		tokens,
		originalText,
		enrichment,
		targetLang: 'ru'
	};
}

beforeEach(() => {
	resetCaptureSession();
	h.enqueueOutbox.mockClear();
	h.captureError.mockClear();
	h.vocabRows.clear();
});

describe('buildLookupEvent — words', () => {
	it('records the tapped position as the occurrence identity', () => {
		const event = buildLookupEvent(input(wordContent))!;
		expect(event.kind).toBe('word');
		expect(event.span_start).toBe(4);
		expect(event.span_end).toBe(4);
		expect(event.article_id).toBe('a1');
		expect(event.article_title).toBe('The Economist');
	});

	it('keys on the token lemma, so an inflection folds onto the base entry', () => {
		const inflected: PopoverContent = { ...wordContent, tokenIndex: 3, original: 'remarkably' };
		const event = buildLookupEvent(input(inflected))!;
		expect(event.entry_key).toBe('word:ru:remarkable');
		expect(event.lemma).toBe('remarkable');
		// The exact article text is preserved separately.
		expect(event.surface).toBe('remarkably');
	});

	it('falls back to the lowercased text when the token carries no lemma', () => {
		const event = buildLookupEvent(input(wordContent))!;
		expect(event.entry_key).toBe('word:ru:resilient');
	});

	it('bakes the target language into the key', () => {
		const event = buildLookupEvent({ ...input(wordContent), targetLang: 'de' })!;
		expect(event.entry_key).toBe('word:de:resilient');
	});

	it('carries the CEFR level but no phrase type', () => {
		const event = buildLookupEvent(input(wordContent))!;
		expect(event.cefr_level).toBe('B2');
		expect(event.phrase_type).toBeUndefined();
	});
});

describe('buildLookupEvent — phrases', () => {
	it('keys on the joined lemmas of the range', () => {
		const event = buildLookupEvent(input(phraseContent))!;
		expect(event.entry_key).toBe('phrase:ru:prove remarkable');
		expect(event.kind).toBe('phrase');
		expect(event.span_start).toBe(2);
		expect(event.span_end).toBe(3);
	});

	it('carries the phrase type but no CEFR level', () => {
		const event = buildLookupEvent(input(phraseContent))!;
		expect(event.phrase_type).toBe('idiom');
		expect(event.cefr_level).toBeUndefined();
	});

	it('records the verbatim phrase text as the surface form', () => {
		expect(buildLookupEvent(input(phraseContent))!.surface).toBe('proved remarkably');
	});
});

describe('buildLookupEvent — edge cases', () => {
	it('returns null for an out-of-range token index', () => {
		const bad: PopoverContent = { ...wordContent, tokenIndex: 99 };
		expect(buildLookupEvent(input(bad))).toBeNull();
	});

	it('returns null when the base normalizes to nothing', () => {
		const punctuationOnly: Token[] = [{ index: 0, text: '—', start: 0, end: 1 }];
		const content: PopoverContent = { ...wordContent, tokenIndex: 0, original: '—' };
		expect(buildLookupEvent({ ...input(content), tokens: punctuationOnly })).toBeNull();
	});

	it('stamps occurred_at from the supplied clock', () => {
		const when = new Date('2026-06-10T12:00:00.000Z');
		expect(buildLookupEvent(input(wordContent), when)!.occurred_at).toBe(
			'2026-06-10T12:00:00.000Z'
		);
	});
});

describe('contextFor', () => {
	it('returns the covering sentence', () => {
		expect(contextFor(4, tokens, originalText, enrichment)).toBe(
			'The market proved remarkably resilient'
		);
	});

	it('picks the sentence that actually covers the token', () => {
		expect(contextFor(6, tokens, originalText, enrichment)).toBe('Everyone noticed');
	});

	it('is empty when no sentence covers the token', () => {
		const noSentences: Enrichment = { ...enrichment, sentences: [] };
		expect(contextFor(4, tokens, originalText, noSentences)).toBe('');
	});
});

describe('truncateOnWordBoundary', () => {
	it('leaves short text untouched', () => {
		expect(truncateOnWordBoundary('short', 300)).toBe('short');
	});

	it('cuts on a word boundary and marks the elision', () => {
		const got = truncateOnWordBoundary('alpha beta gamma delta', 14);
		expect(got).toBe('alpha beta…');
		expect(got.length).toBeLessThanOrEqual(15);
	});

	it('hard-cuts a single very long word rather than truncating to nothing', () => {
		const got = truncateOnWordBoundary('a'.repeat(50), 10);
		expect(got).toBe('a'.repeat(10) + '…');
	});

	it('respects MAX_CONTEXT_LEN in captured context', () => {
		const long = 'word '.repeat(200).trim();
		const longTokens: Token[] = [{ index: 0, text: 'word', start: 0, end: 4 }];
		const longEnrichment: Enrichment = {
			...enrichment,
			sentences: [{ start_index: 0, end_index: 0, translation: '' }]
		};
		// sliceText uses the token offsets, so build the expectation the same way.
		const got = contextFor(0, longTokens, long, longEnrichment);
		expect(got.length).toBeLessThanOrEqual(MAX_CONTEXT_LEN + 1);
	});
});

describe('captureLookup', () => {
	it('queues the event and seeds a local aggregate row', async () => {
		const event = await captureLookup(input(wordContent));

		expect(event).not.toBeNull();
		expect(h.enqueueOutbox).toHaveBeenCalledWith(
			'lookup',
			expect.objectContaining({
				entry_key: 'word:ru:resilient'
			})
		);

		const row = h.vocabRows.get('word:ru:resilient')!;
		// The local row mirrors SERVER state, so a brand-new entry starts at 0:
		// the pending outbox row supplies the +1 the UI displays (§6.2).
		expect(row.count).toBe(0);
		expect(row.updated_at).toBe('');
		expect(row.latest_translation).toBe('устойчивый');
	});

	it('does not queue a second row for the same position', async () => {
		await captureLookup(input(wordContent));
		const second = await captureLookup(input(wordContent));

		expect(second).toBeNull();
		expect(h.enqueueOutbox).toHaveBeenCalledTimes(1);
	});

	it('treats the same lemma at a different position as a new occurrence', async () => {
		await captureLookup(input(wordContent));
		const other: PopoverContent = { ...wordContent, tokenIndex: 1, original: 'market' };
		await captureLookup(input(other));

		expect(h.enqueueOutbox).toHaveBeenCalledTimes(2);
	});

	it('clears the dedup set between articles', async () => {
		await captureLookup(input(wordContent));
		resetCaptureSession();
		await captureLookup(input(wordContent));

		expect(h.enqueueOutbox).toHaveBeenCalledTimes(2);
	});

	it('reports a queue failure and stays retryable rather than throwing', async () => {
		h.enqueueOutbox.mockRejectedValueOnce(new Error('QuotaExceededError'));

		await expect(captureLookup(input(wordContent))).resolves.toBeNull();
		expect(h.captureError).toHaveBeenCalled();

		// The dedup marker was rolled back, so a later tap can try again.
		h.enqueueOutbox.mockResolvedValueOnce(1);
		expect(await captureLookup(input(wordContent))).not.toBeNull();
	});

	it('refreshes only the latest_* facets of an existing row', async () => {
		h.vocabRows.set('word:ru:resilient', {
			entry_key: 'word:ru:resilient',
			kind: 'word',
			lemma: 'resilient',
			target_lang: 'ru',
			surface_forms: ['resilient'],
			count: 7,
			first_seen: 'f',
			last_seen: 'l',
			latest_translation: 'старый перевод',
			latest_context: '',
			latest_article_id: 'old',
			latest_article_title: 'Old',
			updated_at: 'srv'
		});

		await captureLookup(input(wordContent));

		const row = h.vocabRows.get('word:ru:resilient')!;
		expect(row.count).toBe(7); // server-owned, untouched
		expect(row.updated_at).toBe('srv');
		expect(row.latest_translation).toBe('устойчивый');
		expect(row.latest_article_title).toBe('The Economist');
	});
});
