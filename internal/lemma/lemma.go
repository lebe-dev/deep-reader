// Package lemma implements ports.Lemmatizer: it maps an inflected English word
// form to its dictionary lemma so the vocabulary cache can be keyed on lemmas
// rather than on raw string forms (WORD-CACHE-ARCH.md §4).
//
// # Where it runs
//
// Server-side, once, in the fetch stage — right after tokenize.Tokenize and
// before the tokens are persisted. Every consumer (capture, reader overlay,
// prompt narrowing, enrichment post-filter) then reads a lemma that is already
// in the token, so there is exactly one implementation to test and no chance of
// client/server morphology drift.
//
// # Dictionary licensing — NOTICE
//
// The golem library is MIT, but its dictionaries derive from
// michmech/lemmatization-lists, which is ODbL. Embedding the dictionary in the
// distributed binary requires attribution and a statement of the dictionary's
// licence; see THIRD-PARTY.md. This does not affect the MIT licence of Deep
// Reader's own code.
package lemma

import (
	"fmt"
	"strings"

	"github.com/aaaton/golem/v4"
	"github.com/aaaton/golem/v4/dicts/en"

	"deep-reader/internal/model"
	"deep-reader/internal/ports"
)

// CurrentVersion is the lemmatizer generation stamped into
// articles.lemma_version. Bump it whenever the dictionary or the rules change:
// the startup backfill then re-annotates every article whose stored version is
// lower (WORD-CACHE-ARCH.md §4.5).
const CurrentVersion = 1

// Lemmatizer is the golem-backed ports.Lemmatizer. It is safe for concurrent
// use: golem's dictionary is read-only after construction.
type Lemmatizer struct {
	inner *golem.Lemmatizer
}

// New builds a Lemmatizer over golem's embedded English dictionary. It is
// expensive (the dictionary is decompressed into memory) and should be called
// once, at wiring time.
func New() (*Lemmatizer, error) {
	inner, err := golem.New(en.New())
	if err != nil {
		return nil, fmt.Errorf("lemma: load english dictionary: %w", err)
	}
	return &Lemmatizer{inner: inner}, nil
}

// Lemma returns the dictionary form of word, lowercased. An unknown form — a
// neologism, a proper noun, domain jargon — comes back as its own lowercased
// self, so callers never have to special-case a miss.
func (l *Lemmatizer) Lemma(word string) string {
	lower := strings.ToLower(word)
	if lower == "" {
		return ""
	}
	// golem only knows alphabetic forms; anything else (numbers, hyphenated
	// compounds it has never seen) round-trips unchanged anyway, but skipping
	// the lookup keeps the hot path cheap.
	if !isAlpha(lower) {
		return lower
	}
	if got := l.inner.Lemma(lower); got != "" {
		return strings.ToLower(got)
	}
	return lower
}

// Annotate fills Token.Lemma in place and returns the same slice. The lemma is
// left EMPTY whenever it equals the lowercased text — the common case — which
// is what keeps the payload cost of shipping lemmas in the low teens of a
// percent (§4.4). Consumers must therefore read it as `Lemma or lower(Text)`.
//
// It never re-tokenizes: token count, indices and byte offsets are preserved
// exactly, because every enrichment reference indexes into this slice.
func (l *Lemmatizer) Annotate(tokens []model.Token) []model.Token {
	for i := range tokens {
		lower := strings.ToLower(tokens[i].Text)
		lemma := l.Lemma(tokens[i].Text)
		if lemma == lower {
			tokens[i].Lemma = ""
			continue
		}
		tokens[i].Lemma = lemma
	}
	return tokens
}

// Apply annotates tokens with l, tolerating a nil lemmatizer — the pipeline
// packages call it right after tokenization, and a deployment (or a test) that
// wires no lemmatizer must still produce valid, un-annotated tokens rather than
// panic. Consumers read `Lemma or lower(Text)` either way.
func Apply(l ports.Lemmatizer, tokens []model.Token) []model.Token {
	if l == nil {
		return tokens
	}
	return l.Annotate(tokens)
}

// isAlpha reports whether s consists solely of ASCII letters.
func isAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}
