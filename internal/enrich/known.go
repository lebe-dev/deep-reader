// known.go implements vocabulary-aware enrichment: the LLM should not spend
// attention (or tokens) on words the user has already looked up
// (WORD-CACHE-ARCH.md §9).
//
// Two mechanisms, deliberately asymmetric:
//
//   - The PROMPT gets a narrowed list — only terms that occur in the tokens
//     actually being sent, capped — because a 2 000-word list would cost more
//     than the annotations it saves.
//   - The POST-FILTER uses the FULL vocabulary and is the authoritative one.
//     Prompt instructions are a hint; Go code deciding what to persist is not.
//     The feature therefore works against a model that ignores the instruction
//     and against a stale prompt cached inside a provider.
//
// Sentence translations are never affected — only difficult_words and phrases.
package enrich

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	"deep-reader/internal/model"
	"deep-reader/internal/vocab"
)

// maxKnownTermsInPrompt caps how many terms are injected into one request.
// Ordered by occurrence count descending, so the most established vocabulary
// wins the slots.
const maxKnownTermsInPrompt = 200

// knownVocabulary is the per-article view of the user's collected words: the
// key set the post-filter matches against, plus the entries the prompt
// narrowing ranks. It is loaded once per article, not per chunk.
type knownVocabulary struct {
	// keys is every live entry key — the authoritative filter set.
	keys map[string]bool
	// byKey maps an entry key back to its aggregate, for count-based ranking.
	byKey map[string]model.VocabEntry
	// surfaces maps a normalized observed surface form to its entry key. It is
	// the out-of-vocabulary fallback: when the lemmatizer did not recognise a
	// form, the key was built from something the raw token will not fold onto
	// (WORD-CACHE-ARCH.md §3.4).
	surfaces map[string]string
}

// loadKnownVocabulary reads the live vocabulary aggregates once per article.
// It returns an empty set — never an error — when the feature is disabled or
// the read fails: a vocabulary hiccup must degrade to "annotate everything",
// which is the pre-feature behaviour, not fail the enrichment.
func (p *Pool) loadKnownVocabulary(ctx context.Context, settings model.Settings, log *slog.Logger) knownVocabulary {
	if !settings.VocabAssist {
		return knownVocabulary{}
	}
	entries, err := p.store.ListKnownVocab(ctx)
	if err != nil {
		log.Warn("enrich: known vocabulary unavailable, annotating everything", "err", err)
		return knownVocabulary{}
	}
	return buildKnownVocabulary(entries)
}

// buildKnownVocabulary indexes the aggregates for lookup. Exported through
// export_test.go for unit tests.
func buildKnownVocabulary(entries []model.VocabEntry) knownVocabulary {
	k := knownVocabulary{
		keys:     make(map[string]bool, len(entries)),
		byKey:    make(map[string]model.VocabEntry, len(entries)),
		surfaces: map[string]string{},
	}
	for _, e := range entries {
		if e.EntryKey == "" {
			continue
		}
		k.keys[e.EntryKey] = true
		k.byKey[e.EntryKey] = e
		for _, form := range e.SurfaceForms {
			if form == "" {
				continue
			}
			// First writer wins: entries are ranked count-desc, so a form shared
			// by two entries resolves to the better-established one.
			if _, exists := k.surfaces[form]; !exists {
				k.surfaces[form] = e.EntryKey
			}
		}
	}
	return k
}

// knowsWord reports whether the token is already in the user's vocabulary,
// matching by lemma first and falling back to the observed surface forms.
func (k knownVocabulary) knowsWord(targetLang string, t model.Token) bool {
	if len(k.keys) == 0 {
		return false
	}
	if k.keys[vocab.WordKey(targetLang, t)] {
		return true
	}
	// Out-of-vocabulary fallback: the entry may have been keyed on the model's
	// lemma, which this raw form will not fold onto.
	key, ok := k.surfaces[vocab.Normalize(t.Text)]
	return ok && k.byKey[key].Kind == model.LookupKindWord
}

// knowsPhrase reports whether the inclusive token range is already known.
func (k knownVocabulary) knowsPhrase(targetLang string, tokens []model.Token, start, end int) bool {
	if len(k.keys) == 0 {
		return false
	}
	key := vocab.PhraseKey(targetLang, tokens, start, end)
	return key != "" && k.keys[key]
}

// narrowForTokens intersects the vocabulary with the lemmas of the tokens
// actually being sent in this request and returns at most
// maxKnownTermsInPrompt display terms, most-established first.
//
// The intersection is exact now that tokens carry lemmas; a surface-form
// intersection would have missed every inflected occurrence, which is precisely
// the case this feature exists to handle.
func (k knownVocabulary) narrowForTokens(targetLang string, tokens []model.Token) []string {
	if len(k.keys) == 0 || len(tokens) == 0 {
		return nil
	}

	matched := map[string]model.VocabEntry{}
	for _, t := range tokens {
		key := vocab.WordKey(targetLang, t)
		if entry, ok := k.byKey[key]; ok {
			matched[key] = entry
			continue
		}
		if skey, ok := k.surfaces[vocab.Normalize(t.Text)]; ok {
			matched[skey] = k.byKey[skey]
		}
	}
	// Phrases: a phrase entry is relevant when its first lemma appears in the
	// chunk. Checking full sequences here would duplicate the overlay's matcher
	// for no gain — an over-inclusive prompt list is harmless, an incomplete one
	// is not.
	firstLemmas := map[string]bool{}
	for _, t := range tokens {
		firstLemmas[vocab.LemmaOf(t)] = true
	}
	for key, entry := range k.byKey {
		if entry.Kind != model.LookupKindPhrase {
			continue
		}
		if head := firstLemmaOfKey(key); head != "" && firstLemmas[head] {
			matched[key] = entry
		}
	}

	entries := make([]model.VocabEntry, 0, len(matched))
	for _, e := range matched {
		entries = append(entries, e)
	}
	// Count desc, then key asc so the prompt is deterministic for a given
	// vocabulary — an unstable prompt would defeat provider-side caching.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Count != entries[j].Count {
			return entries[i].Count > entries[j].Count
		}
		return entries[i].EntryKey < entries[j].EntryKey
	})
	if len(entries) > maxKnownTermsInPrompt {
		entries = entries[:maxKnownTermsInPrompt]
	}

	terms := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Lemma != "" {
			terms = append(terms, e.Lemma)
		}
	}
	return terms
}

// firstLemmaOfKey returns the first lemma of a phrase entry key
// ("phrase:ru:spill the bean" → "spill"), or "" for a malformed key.
func firstLemmaOfKey(key string) string {
	parts := strings.SplitN(key, ":", 3)
	if len(parts) < 3 {
		return ""
	}
	base, _, _ := strings.Cut(parts[2], " ")
	return base
}

// knownFilter binds a vocabulary to the target language so sanitizeEnrichment
// can ask "is this already known?" without carrying two parameters. Its zero
// value is a filter that knows nothing, which is exactly what callers with no
// vocabulary (or with vocab_assist off) want.
type knownFilter struct {
	known      knownVocabulary
	targetLang string
}

// knowsWord reports whether the token is already in the user's vocabulary.
func (f knownFilter) knowsWord(t model.Token) bool {
	return f.known.knowsWord(f.targetLangOrDefault(), t)
}

// knowsPhrase reports whether the inclusive token range is already known.
func (f knownFilter) knowsPhrase(tokens []model.Token, start, end int) bool {
	return f.known.knowsPhrase(f.targetLangOrDefault(), tokens, start, end)
}

// targetLangOrDefault guards against a settings row that predates the field.
// The entry key embeds the language, so getting it wrong would silently match
// nothing rather than fail loudly.
func (f knownFilter) targetLangOrDefault() string {
	if f.targetLang == "" {
		return model.DefaultTargetLanguage
	}
	return f.targetLang
}

// tokensInSpan returns the tokens the inclusive span covers, clamped to the
// slice. The narrowing intersects against exactly what the request sends, so a
// chunk's list never mentions a word the model cannot see.
func tokensInSpan(tokens []model.Token, span model.Span) []model.Token {
	start := max(span.Start, 0)
	end := min(span.End, len(tokens)-1)
	if start > end {
		return nil
	}
	return tokens[start : end+1]
}

// tokensInSpans is tokensInSpan over several spans (the top-up path sends a
// set of gaps in one request).
func tokensInSpans(tokens []model.Token, spans []model.Span) []model.Token {
	var out []model.Token
	for _, span := range spans {
		out = append(out, tokensInSpan(tokens, span)...)
	}
	return out
}
