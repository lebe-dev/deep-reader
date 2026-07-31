// Package vocab holds the entry-key algebra of the word cache: the one
// normalization and the one key format shared by everything that touches the
// vocabulary (WORD-CACHE-ARCH.md §3.3).
//
// There is deliberately ONE normalization in the codebase. enrich reuses
// Normalize for its phrase-echo checks, the enrichment post-filter builds keys
// with Key, and the TypeScript side (frontend/src/lib/vocab/key.ts) mirrors both
// against a shared golden table. Do not invent a second one.
package vocab

import (
	"strings"
	"unicode"

	"deep-reader/internal/model"
)

// Normalize lowercases s, keeps letters and digits, and collapses everything
// else into single spaces (trimming the edges). It is the canonical form the
// entry key is built from and what surface forms are stored as.
func Normalize(s string) string {
	var b strings.Builder
	pendingSpace := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if pendingSpace && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			pendingSpace = false
			continue
		}
		pendingSpace = true
	}
	return b.String()
}

// Key builds the aggregate key `kind:targetLang:normalize(base)`, e.g.
// "word:ru:abduct" or "phrase:ru:spill the bean".
//
// For a word, base is the token's lemma; for a phrase, the space-joined lemmas
// of its token range, so "spilled the beans" and "spilling the beans" land on
// one entry. The client computes this key and sends it; the server recomputes it
// the same way for the enrichment post-filter, so the two sides can only
// disagree about the (golden-tested) normalization — never about morphology,
// which is computed server-side once.
func Key(kind, targetLang, base string) string {
	return kind + ":" + targetLang + ":" + Normalize(base)
}

// LemmaOf returns the token's lemma, falling back to its lowercased text.
// model.Token.Lemma is omitted when it equals the lowercased text, so a raw read
// of the field is always a bug — go through this.
func LemmaOf(t model.Token) string {
	if t.Lemma != "" {
		return t.Lemma
	}
	return strings.ToLower(t.Text)
}

// WordKey builds the entry key for a single token.
func WordKey(targetLang string, t model.Token) string {
	return Key(model.LookupKindWord, targetLang, LemmaOf(t))
}

// PhraseKey builds the entry key for the inclusive token range
// [start, end] — the space-joined lemmas of the tokens it covers. Out-of-range
// or inverted spans yield an empty base, which callers treat as "no key".
func PhraseKey(targetLang string, tokens []model.Token, start, end int) string {
	if start < 0 || end < start || end >= len(tokens) {
		return ""
	}
	parts := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		parts = append(parts, LemmaOf(tokens[i]))
	}
	return Key(model.LookupKindPhrase, targetLang, strings.Join(parts, " "))
}
