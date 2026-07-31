/**
 * The vocabulary overlay (WORD-CACHE-ARCH.md §10).
 *
 * Because the LLM no longer annotates words the user has already looked up, the
 * reader supplies them itself — in EVERY article containing them, in ANY
 * inflection, including articles enriched long before the word was collected.
 * No re-enrichment and no payload rewrite: the overlay is computed at render
 * time from the local vocabulary.
 *
 * Everything here is pure so it can be unit-tested without a DOM.
 */

import type { Token, VocabEntry } from '$lib/types';
import { lemmaOf, normalize } from './key';

/**
 * The matcher indices, built once per (article, vocabulary) pair rather than per
 * click.
 */
export interface VocabIndex {
	/** lemma → entry, the primary word matcher. */
	words: Map<string, VocabEntry>;
	/**
	 * Normalized surface form → entry. The out-of-vocabulary fallback: when the
	 * lemmatizer did not recognise a form (neologisms, proper nouns, jargon), the
	 * key may have come from the model's lemma and the raw form will not fold
	 * onto it (§3.4).
	 */
	wordSurfaces: Map<string, VocabEntry>;
	/** First lemma of a phrase → candidate entries, longest sequence first. */
	phrases: Map<string, PhraseCandidate[]>;
}

/** A phrase entry pre-split into the lemma sequence the matcher compares. */
export interface PhraseCandidate {
	entry: VocabEntry;
	/** The entry key's lemma sequence, e.g. ['spill', 'the', 'bean']. */
	lemmas: string[];
}

/** A phrase occurrence found in the article's tokens. */
export interface PhraseMatch {
	entry: VocabEntry;
	startIndex: number;
	endIndex: number;
}

/** An empty index — what the reader uses when the overlay is off. */
export function emptyVocabIndex(): VocabIndex {
	return { words: new Map(), wordSurfaces: new Map(), phrases: new Map() };
}

/**
 * Build the matcher indices from the local vocabulary. Tombstoned entries are
 * skipped: a deleted word must leave the overlay immediately.
 */
export function buildVocabIndex(entries: VocabEntry[]): VocabIndex {
	const index = emptyVocabIndex();

	for (const entry of entries) {
		if (entry.deleted_at) continue;

		if (entry.kind === 'phrase') {
			const lemmas = phraseLemmasOf(entry);
			if (lemmas.length === 0) continue;
			const head = lemmas[0];
			const bucket = index.phrases.get(head) ?? [];
			bucket.push({ entry, lemmas });
			index.phrases.set(head, bucket);
			continue;
		}

		const lemma = normalize(entry.lemma);
		if (lemma !== '' && !index.words.has(lemma)) index.words.set(lemma, entry);
		for (const form of entry.surface_forms ?? []) {
			const norm = normalize(form);
			if (norm !== '' && !index.wordSurfaces.has(norm)) index.wordSurfaces.set(norm, entry);
		}
	}

	// Longest first, so "spill the beans" wins over a hypothetical "spill".
	for (const bucket of index.phrases.values()) {
		bucket.sort((a, b) => b.lemmas.length - a.lemmas.length);
	}
	return index;
}

/**
 * The lemma sequence a phrase entry matches on, taken from its entry key
 * (`phrase:ru:spill the bean`) because that is the normalized, lemmatized form —
 * `lemma` holds the verbatim display text.
 */
function phraseLemmasOf(entry: VocabEntry): string[] {
	const parts = entry.entry_key.split(':');
	const base = parts.length >= 3 ? parts.slice(2).join(':') : normalize(entry.lemma);
	return base.split(' ').filter((p) => p !== '');
}

/**
 * Find the vocabulary entry for a single token: by lemma first, then by observed
 * surface form.
 */
export function matchWord(index: VocabIndex, token: Token): VocabEntry | undefined {
	const byLemma = index.words.get(normalize(lemmaOf(token)));
	if (byLemma) return byLemma;
	return index.wordSurfaces.get(normalize(token.text));
}

/**
 * Find the phrase occurrence covering `tokenIndex`, if any.
 *
 * Candidates are keyed by their first lemma, so this scans backwards over at
 * most the longest candidate's length rather than over the whole article.
 */
export function matchPhraseAt(
	index: VocabIndex,
	tokens: Token[],
	tokenIndex: number
): PhraseMatch | undefined {
	if (index.phrases.size === 0) return undefined;

	let longest = 0;
	for (const bucket of index.phrases.values()) {
		if (bucket.length > 0) longest = Math.max(longest, bucket[0].lemmas.length);
	}

	// A phrase covering tokenIndex can start at most `longest - 1` tokens before it.
	for (let start = tokenIndex; start >= Math.max(0, tokenIndex - longest + 1); start--) {
		const head = normalize(lemmaOf(tokens[start]));
		const candidates = index.phrases.get(head);
		if (!candidates) continue;
		for (const candidate of candidates) {
			const end = start + candidate.lemmas.length - 1;
			// Longest-first ordering means the first full match wins.
			if (end < tokenIndex || end >= tokens.length) continue;
			if (!sequenceMatches(tokens, start, candidate.lemmas)) continue;
			return { entry: candidate.entry, startIndex: start, endIndex: end };
		}
	}
	return undefined;
}

/** Whether the tokens starting at `start` have exactly this lemma sequence. */
function sequenceMatches(tokens: Token[], start: number, lemmas: string[]): boolean {
	for (let i = 0; i < lemmas.length; i++) {
		const token = tokens[start + i];
		if (!token) return false;
		if (normalize(lemmaOf(token)) !== lemmas[i]) return false;
	}
	return true;
}

/**
 * The set of token indices the overlay decorates in this article: every token
 * matched as a known word, plus every token inside a matched phrase.
 *
 * Tokens already annotated by the enrichment are EXCLUDED — enrichment always
 * outranks the overlay (its translation is contextual to this article), so
 * decorating them twice would be misleading.
 */
export function buildOverlayIndices(
	index: VocabIndex,
	tokens: Token[],
	annotated: (tokenIndex: number) => boolean
): Set<number> {
	const marked = new Set<number>();
	if (index.words.size === 0 && index.wordSurfaces.size === 0 && index.phrases.size === 0) {
		return marked;
	}

	for (let i = 0; i < tokens.length; i++) {
		if (annotated(i)) continue;

		const phrase = matchPhraseAt(index, tokens, i);
		if (phrase) {
			for (let k = phrase.startIndex; k <= phrase.endIndex; k++) {
				if (!annotated(k)) marked.add(k);
			}
			continue;
		}
		if (matchWord(index, tokens[i])) marked.add(i);
	}
	return marked;
}
