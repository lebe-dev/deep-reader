/**
 * The entry-key algebra of the word cache, mirroring `internal/vocab/key.go`
 * exactly (WORD-CACHE-ARCH.md §3.3).
 *
 * The client computes an entry key from the lemma the server already put in the
 * token and sends it; the server recomputes the same key for the enrichment
 * post-filter. Because morphology is computed once, server-side, the two sides
 * can only disagree about this normalization — which is why both
 * implementations are pinned by one shared golden table
 * (`internal/vocab/testdata/normalize_golden.json`). Do not add a second
 * normalization anywhere.
 */

import type { LookupKind, Token } from '$lib/types';

/**
 * Lowercase, keep letters and digits, collapse everything else into single
 * spaces, trim. The canonical form entry keys are built from and surface forms
 * are stored as.
 *
 * `\p{L}` / `\p{N}` are the Unicode-property equivalents of Go's
 * `unicode.IsLetter` / `unicode.IsNumber`, so accented and non-Latin text
 * normalizes identically on both sides.
 */
export function normalize(input: string): string {
	return input
		.toLowerCase()
		.replace(/[^\p{L}\p{N}]+/gu, ' ')
		.trim();
}

/**
 * Build the aggregate key `kind:targetLang:normalize(base)`, e.g.
 * `word:ru:abduct` or `phrase:ru:spill the bean`.
 */
export function buildEntryKey(kind: LookupKind, targetLang: string, base: string): string {
	return `${kind}:${targetLang}:${normalize(base)}`;
}

/**
 * The token's lemma, falling back to its lowercased text.
 *
 * `Token.lemma` is omitted whenever it equals the lowercased text, so a raw
 * read of the field is always a bug — every lemma read goes through here.
 */
export function lemmaOf(token: Token): string {
	return token.lemma !== undefined && token.lemma !== '' ? token.lemma : token.text.toLowerCase();
}

/** Entry key for a single tapped token. */
export function wordEntryKey(targetLang: string, token: Token): string {
	return buildEntryKey('word', targetLang, lemmaOf(token));
}

/**
 * Entry key for the inclusive token range `[start, end]` — the space-joined
 * lemmas of the tokens it covers, so "spilled the beans" and "spilling the
 * beans" land on one entry. Returns an empty string for an out-of-range or
 * inverted span, which callers treat as "no key".
 */
export function phraseEntryKey(
	targetLang: string,
	tokens: Token[],
	start: number,
	end: number
): string {
	if (start < 0 || end < start || end >= tokens.length) return '';
	const parts: string[] = [];
	for (let i = start; i <= end; i++) parts.push(lemmaOf(tokens[i]));
	return buildEntryKey('phrase', targetLang, parts.join(' '));
}
