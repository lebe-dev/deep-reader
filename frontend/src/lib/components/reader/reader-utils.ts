// Reader utility functions — all pure, no side effects.
// Named exports only (project convention).

import type { DifficultWord, Enrichment, GlossaryItem, Phrase, Sentence, Token } from '$lib/types';

// ---------------------------------------------------------------------------
// Lookup maps — built once per article load.
// ---------------------------------------------------------------------------

/** Pre-built index: token_index → DifficultWord for O(1) lookup. */
export type DifficultWordMap = Map<number, DifficultWord>;

/** Pre-built index: token_index → Phrase (any phrase that covers this index). */
export type PhraseMap = Map<number, Phrase>;

export function buildDifficultWordMap(enrichment: Enrichment): DifficultWordMap {
	const map: DifficultWordMap = new Map();
	for (const dw of enrichment.difficult_words) {
		map.set(dw.token_index, dw);
	}
	return map;
}

export function buildPhraseMap(enrichment: Enrichment): PhraseMap {
	const map: PhraseMap = new Map();
	for (const phrase of enrichment.phrases) {
		// Every token index in [start_index, end_index] maps to this phrase.
		for (let i = phrase.start_index; i <= phrase.end_index; i++) {
			// If multiple phrases overlap (edge case), keep the first one.
			if (!map.has(i)) map.set(i, phrase);
		}
	}
	return map;
}

// ---------------------------------------------------------------------------
// Token-range reconstruction
// ---------------------------------------------------------------------------

/**
 * Reconstruct the display text for a range of tokens [startIdx, endIdx],
 * preserving the exact whitespace/punctuation that sits between them in
 * originalText.
 *
 * Token `start`/`end` are *byte* offsets into the UTF-8 encoding of
 * originalText (Go semantics), which do NOT match JS string (UTF-16) indices
 * once the text contains any non-ASCII character (e.g. typographic ' or —).
 * Slicing the encoded byte array and decoding back is the only correct way to
 * map those offsets to a substring.
 */
export function sliceText(
	tokens: Token[],
	startIdx: number,
	endIdx: number,
	originalText: string
): string {
	if (!tokens.length) return '';
	const first = tokens[startIdx];
	const last = tokens[endIdx];
	if (!first || !last) return '';
	const bytes = new TextEncoder().encode(originalText);
	return new TextDecoder().decode(bytes.subarray(first.start, last.end));
}

// ---------------------------------------------------------------------------
// Sentence lookup
// ---------------------------------------------------------------------------

/**
 * Find the sentence that best covers a token index.
 * Returns the tightest covering sentence (smallest range that still contains idx).
 */
export function findCoveringSentence(
	tokenIndex: number,
	sentences: Sentence[]
): Sentence | undefined {
	let best: Sentence | undefined;
	for (const s of sentences) {
		if (tokenIndex >= s.start_index && tokenIndex <= s.end_index) {
			if (!best || s.end_index - s.start_index < best.end_index - best.start_index) {
				best = s;
			}
		}
	}
	return best;
}

/**
 * Find the sentence that best covers a token range [selStart, selEnd].
 * Returns the tightest sentence spanning the entire selection.
 */
export function findCoveringSentenceForRange(
	selStart: number,
	selEnd: number,
	sentences: Sentence[]
): Sentence | undefined {
	let best: Sentence | undefined;
	for (const s of sentences) {
		if (s.start_index <= selStart && s.end_index >= selEnd) {
			if (!best || s.end_index - s.start_index < best.end_index - best.start_index) {
				best = s;
			}
		}
	}
	return best;
}

// ---------------------------------------------------------------------------
// Glossary lookup
// ---------------------------------------------------------------------------

/**
 * Find a glossary item whose term appears in the given phrase text.
 * Case-insensitive substring match.
 */
export function findGlossaryItem(
	phraseText: string,
	glossary: GlossaryItem[]
): GlossaryItem | undefined {
	const lower = phraseText.toLowerCase();
	return glossary.find((g) => lower.includes(g.term.toLowerCase()));
}

// ---------------------------------------------------------------------------
// Popover content builders
// ---------------------------------------------------------------------------

export interface WordPopoverContent {
	kind: 'word';
	original: string;
	translation: string;
	lemma: string;
	cefrLevel: string;
}

export interface PhrasePopoverContent {
	kind: 'phrase';
	original: string;
	phraseType: string;
	translationOrDefinition: string;
	/** token range for highlighting */
	startIndex: number;
	endIndex: number;
}

export interface SentenceSheetContent {
	kind: 'sentence';
	original: string;
	translation: string;
}

export type PopoverContent = WordPopoverContent | PhrasePopoverContent;

/**
 * Determine what to show when the user clicks/taps a word token at `tokenIndex`.
 * Phrase takes priority over single word.
 */
export function resolveClickContent(
	tokenIndex: number,
	tokens: Token[],
	originalText: string,
	difficultWordMap: DifficultWordMap,
	phraseMap: PhraseMap
): PopoverContent | null {
	// Phrase takes priority.
	const phrase = phraseMap.get(tokenIndex);
	if (phrase) {
		return {
			kind: 'phrase',
			original: sliceText(tokens, phrase.start_index, phrase.end_index, originalText),
			phraseType: phrase.type,
			translationOrDefinition: phrase.translation_or_definition,
			startIndex: phrase.start_index,
			endIndex: phrase.end_index
		};
	}

	// Single difficult word.
	const dw = difficultWordMap.get(tokenIndex);
	if (dw) {
		const token = tokens[tokenIndex];
		return {
			kind: 'word',
			original: token?.text ?? '',
			translation: dw.translation,
			lemma: dw.lemma,
			cefrLevel: dw.cefr_level
		};
	}

	return null;
}

// ---------------------------------------------------------------------------
// Debounce helper (used for progress tracking)
// ---------------------------------------------------------------------------

export function debounce<T extends (...args: Parameters<T>) => void>(
	fn: T,
	delayMs: number
): (...args: Parameters<T>) => void {
	let timer: ReturnType<typeof setTimeout> | undefined;
	return (...args: Parameters<T>) => {
		clearTimeout(timer);
		timer = setTimeout(() => fn(...args), delayMs);
	};
}
