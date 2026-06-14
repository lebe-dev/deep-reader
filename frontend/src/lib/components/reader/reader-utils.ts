// Reader utility functions — all pure, no side effects.
// Named exports only (project convention).

import type { DifficultWord, Enrichment, GlossaryItem, Phrase, Sentence, Token } from '$lib/types';

// ---------------------------------------------------------------------------
// Enrichment normalisation
// ---------------------------------------------------------------------------

/**
 * Coerce a server-provided enrichment into one with all four arrays guaranteed.
 *
 * The backend (Go) marshals empty slices as JSON `null`, and an article can be
 * `enriched` with, say, no difficult words at all — so `difficult_words`,
 * `phrases`, `sentences` or `glossary` may each arrive as `null` even though the
 * TypeScript `Enrichment` type declares them non-null. The whole object can also
 * be absent (`enrichment,omitempty`). Reader code maps/iterates these arrays, so
 * a single `null` crashes the render with "Cannot read properties of null
 * (reading 'map')". Normalising once at the boundary lets every downstream
 * consumer rely on the type contract. Stale payloads cached in IndexedDB before
 * this fix are normalised on read too (the reader normalises whatever it loads,
 * cache or network).
 */
export function normalizeEnrichment(enrichment: Enrichment | null | undefined): Enrichment {
	return {
		difficult_words: enrichment?.difficult_words ?? [],
		phrases: enrichment?.phrases ?? [],
		sentences: enrichment?.sentences ?? [],
		glossary: enrichment?.glossary ?? []
	};
}

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
// Render segments (word / gap / markdown image / markdown link)
// ---------------------------------------------------------------------------
//
// The backend tokenizer emits ONLY word tokens (byte offsets into the UTF-8
// encoding of originalText). The reader reconstructs the gaps (whitespace,
// punctuation) between consecutive tokens from originalText itself.
//
// On top of that, raw markdown image (`![alt](url)`) and link (`[text](url)`)
// syntax that survives ingestion is detected here and turned into dedicated
// segments so the reader can render an <img> / <a> instead of leaking the
// markdown source as prose. Tokens that fall inside a detected span are
// dropped from the output (they only existed as fragments of the URL/alt).

/** A single renderable unit produced by {@link buildRenderSegments}. */
export type RenderSegment =
	| { kind: 'word'; text: string; index: number }
	| { kind: 'gap'; text: string }
	| { kind: 'image'; alt: string; url: string }
	| { kind: 'link'; text: string; url: string };

/** A markdown image/link occurrence located in the source text (string indices). */
export interface MarkdownSpan {
	start: number;
	end: number;
	kind: 'image' | 'link';
	/** alt (image) or link text. */
	text: string;
	url: string;
}

// `(!?)[text](url "optional title")` — text has no `]`/newline, url no space/`)`.
const MARKDOWN_SPAN_RE = /(!?)\[([^\]\n]*)\]\(\s*([^)\s]+)(?:\s+[^)]*)?\)/g;

/** Only http(s) URLs are rendered, to avoid javascript:/data: injection. */
export function isHttpUrl(url: string): boolean {
	return /^https?:\/\//i.test(url);
}

/** UTF-8 byte length of a Unicode code point. */
function utf8Len(cp: number): number {
	if (cp < 0x80) return 1;
	if (cp < 0x800) return 2;
	if (cp < 0x10000) return 3;
	return 4;
}

/**
 * Map every UTF-8 byte offset to the JS (UTF-16) string index of the character
 * it belongs to. Token offsets are byte offsets (Go semantics); the reader
 * works in string space, so this bridges the two without re-encoding per slice.
 * The returned array has length byteCount + 1 (the final entry maps the
 * exclusive end offset to text.length).
 */
export function buildByteToStrMap(text: string): number[] {
	const map: number[] = [];
	let byte = 0;
	for (let s = 0; s < text.length; ) {
		const cp = text.codePointAt(s) as number;
		const utf16 = cp > 0xffff ? 2 : 1;
		const bytes = utf8Len(cp);
		for (let k = 0; k < bytes; k++) map[byte + k] = s;
		byte += bytes;
		s += utf16;
	}
	map[byte] = text.length;
	return map;
}

/** Locate markdown image/link spans (with safe http(s) URLs) in source order. */
export function findMarkdownSpans(text: string): MarkdownSpan[] {
	const spans: MarkdownSpan[] = [];
	for (const m of text.matchAll(MARKDOWN_SPAN_RE)) {
		const url = m[3];
		if (!isHttpUrl(url)) continue; // leave unsafe/relative refs as raw prose
		spans.push({
			start: m.index,
			end: m.index + m[0].length,
			kind: m[1] === '!' ? 'image' : 'link',
			text: m[2],
			url
		});
	}
	return spans;
}

/**
 * Build the ordered render segments for an article: word tokens, the
 * reconstructed gaps between them, and any markdown image/link spans.
 *
 * Tokens whose start falls inside a markdown span are omitted (they are URL/alt
 * fragments). Span boundaries always sit on punctuation (`!`, `[`, `)`), which
 * are token split points, so no word token ever straddles a boundary.
 */
export function buildRenderSegments(tokens: Token[], originalText: string): RenderSegment[] {
	if (tokens.length === 0) return [];

	const text = originalText;
	const byteToStr = buildByteToStrMap(text);
	const strIdx = (byte: number): number => byteToStr[byte] ?? text.length;
	const spans = findMarkdownSpans(text);

	const segs: RenderSegment[] = [];
	let ti = 0; // token pointer
	let cursor = 0; // string index of the next unconsumed character

	// Emit word + gap segments for tokens up to (but not including) `hi`.
	const emitWordsUntil = (hi: number) => {
		while (ti < tokens.length) {
			const t = tokens[ti];
			const ts = strIdx(t.start);
			if (ts >= hi) break;
			if (ts > cursor) segs.push({ kind: 'gap', text: text.slice(cursor, ts) });
			segs.push({ kind: 'word', text: t.text, index: t.index });
			cursor = strIdx(t.end);
			ti++;
		}
		if (cursor < hi) {
			segs.push({ kind: 'gap', text: text.slice(cursor, hi) });
			cursor = hi;
		}
	};

	for (const span of spans) {
		if (span.start < cursor) continue; // defensive: skip overlapping match
		emitWordsUntil(span.start);
		segs.push(
			span.kind === 'image'
				? { kind: 'image', alt: span.text, url: span.url }
				: { kind: 'link', text: span.text, url: span.url }
		);
		while (ti < tokens.length && strIdx(tokens[ti].start) < span.end) ti++;
		cursor = span.end;
	}
	emitWordsUntil(text.length);

	return segs;
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
	/** True when the translation was recovered from the glossary. */
	fromGlossary: boolean;
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

/**
 * In-place action menu shown on a long-press over a sentence. Offers copying
 * the sentence and (when a translation exists in the enrichment) opening it.
 */
export interface SentenceMenuContent {
	kind: 'sentence-menu';
	original: string;
	/** Empty string when the enrichment has no translation for this sentence. */
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
			translationOrDefinition: phrase.translation,
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
			cefrLevel: dw.cefr_level,
			fromGlossary: dw.source === 'glossary'
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
