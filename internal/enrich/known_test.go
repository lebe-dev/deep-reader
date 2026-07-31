package enrich_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"deep-reader/internal/enrich"
	"deep-reader/internal/model"
)

// vocabWord builds a live word aggregate for the given lemma.
func vocabWord(lemma string, count int, surfaceForms ...string) model.VocabEntry {
	return model.VocabEntry{
		EntryKey:          "word:ru:" + lemma,
		Kind:              model.LookupKindWord,
		Lemma:             lemma,
		TargetLang:        "ru",
		SurfaceForms:      surfaceForms,
		Count:             count,
		LatestTranslation: "перевод",
	}
}

func vocabPhrase(base string, count int) model.VocabEntry {
	return model.VocabEntry{
		EntryKey:          "phrase:ru:" + base,
		Kind:              model.LookupKindPhrase,
		Lemma:             base,
		TargetLang:        "ru",
		Count:             count,
		LatestTranslation: "перевод",
	}
}

// tok builds a token, optionally carrying a lemma.
func tok(index int, text string, lemma ...string) model.Token {
	t := model.Token{Index: index, Text: text, Start: index * 10, End: index*10 + len(text)}
	if len(lemma) > 0 {
		t.Lemma = lemma[0]
	}
	return t
}

// ── The authoritative post-filter (§9.3) ──────────────────────────────────────

func TestSanitizeDropsKnownWordInAnyInflection(t *testing.T) {
	// "resilient" was collected; the article says "resilience". Because the
	// token carries its lemma, the filter still recognises it — this is the
	// whole point of lemma-keyed vocabulary.
	tokens := []model.Token{
		tok(0, "proved"),
		tok(1, "resilience", "resilient"),
		tok(2, "throughout"),
	}
	e := model.Enrichment{
		DifficultWords: []model.DifficultWord{
			{TokenIndex: 1, Lemma: "resilience", Translation: "устойчивость", CEFRLevel: model.CEFRB2},
			{TokenIndex: 2, Lemma: "throughout", Translation: "на протяжении", CEFRLevel: model.CEFRB2},
		},
	}

	got := enrich.SanitizeEnrichmentKnowing(e, tokens, []model.VocabEntry{vocabWord("resilient", 3)}, "ru")

	if len(got.DifficultWords) != 1 {
		t.Fatalf("kept %d words, want 1: %+v", len(got.DifficultWords), got.DifficultWords)
	}
	if got.DifficultWords[0].TokenIndex != 2 {
		t.Errorf("wrong word survived: %+v", got.DifficultWords[0])
	}
}

func TestSanitizeUsesTokenLemmaNotTheModelsGuess(t *testing.T) {
	// The model claims a different lemma than the token carries. The vocabulary
	// was keyed on the token's lemma, so only that one may decide the match.
	tokens := []model.Token{tok(0, "ran", "run")}
	e := model.Enrichment{
		DifficultWords: []model.DifficultWord{
			{TokenIndex: 0, Lemma: "ranning", Translation: "бежал", CEFRLevel: model.CEFRB2},
		},
	}

	got := enrich.SanitizeEnrichmentKnowing(e, tokens, []model.VocabEntry{vocabWord("run", 2)}, "ru")

	if len(got.DifficultWords) != 0 {
		t.Errorf("a known word survived because the model's lemma was trusted: %+v", got.DifficultWords)
	}
}

func TestSanitizeFallsBackToSurfaceFormsForOutOfVocabularyEntries(t *testing.T) {
	// The lemmatizer did not recognise this proper noun, so the entry key came
	// from elsewhere and the raw form will not fold onto it. surface_forms is
	// the documented fallback (§3.4).
	tokens := []model.Token{tok(0, "Kubernetes")}
	entry := model.VocabEntry{
		EntryKey:     "word:ru:k8s",
		Kind:         model.LookupKindWord,
		Lemma:        "k8s",
		TargetLang:   "ru",
		SurfaceForms: []string{"kubernetes"},
		Count:        1,
	}
	e := model.Enrichment{
		DifficultWords: []model.DifficultWord{
			{TokenIndex: 0, Lemma: "kubernetes", Translation: "кубернетес", CEFRLevel: model.CEFRC1},
		},
	}

	got := enrich.SanitizeEnrichmentKnowing(e, tokens, []model.VocabEntry{entry}, "ru")

	if len(got.DifficultWords) != 0 {
		t.Errorf("surface-form fallback did not match: %+v", got.DifficultWords)
	}
}

func TestSanitizeDropsKnownPhraseInAnyInflection(t *testing.T) {
	tokens := []model.Token{
		tok(0, "He"),
		tok(1, "spilling", "spill"),
		tok(2, "the"),
		tok(3, "beans", "bean"),
	}
	e := model.Enrichment{
		Phrases: []model.Phrase{
			{StartIndex: 1, EndIndex: 3, Type: model.PhraseTypeIdiom,
				Text: "spilling the beans", Translation: "выдал секрет"},
		},
	}

	got := enrich.SanitizeEnrichmentKnowing(e, tokens,
		[]model.VocabEntry{vocabPhrase("spill the bean", 4)}, "ru")

	if len(got.Phrases) != 0 {
		t.Errorf("a known phrase survived: %+v", got.Phrases)
	}
}

func TestSanitizeNeverTouchesSentences(t *testing.T) {
	// Sentences containing known words are still translated — the feature saves
	// word annotations, never comprehension of the text.
	tokens := []model.Token{tok(0, "resilience", "resilient"), tok(1, "matters")}
	e := model.Enrichment{
		DifficultWords: []model.DifficultWord{
			{TokenIndex: 0, Lemma: "resilience", Translation: "устойчивость", CEFRLevel: model.CEFRB2},
		},
		Sentences: []model.Sentence{
			{StartIndex: 0, EndIndex: 1, Translation: "Устойчивость важна."},
		},
	}

	got := enrich.SanitizeEnrichmentKnowing(e, tokens, []model.VocabEntry{vocabWord("resilient", 3)}, "ru")

	if len(got.DifficultWords) != 0 {
		t.Errorf("known word not dropped: %+v", got.DifficultWords)
	}
	if len(got.Sentences) != 1 {
		t.Errorf("sentence translation was filtered: %+v", got.Sentences)
	}
}

func TestSanitizeWithEmptyVocabularyKeepsEverything(t *testing.T) {
	tokens := []model.Token{tok(0, "resilience", "resilient")}
	e := model.Enrichment{
		DifficultWords: []model.DifficultWord{
			{TokenIndex: 0, Lemma: "resilience", Translation: "устойчивость", CEFRLevel: model.CEFRB2},
		},
	}

	got := enrich.SanitizeEnrichmentKnowing(e, tokens, nil, "ru")

	if len(got.DifficultWords) != 1 {
		t.Errorf("an empty vocabulary filtered something: %+v", got.DifficultWords)
	}
}

// ── Prompt narrowing (§9.2) ───────────────────────────────────────────────────

func TestNarrowKnownTermsIntersectsWithTheTokensBeingSent(t *testing.T) {
	entries := []model.VocabEntry{
		vocabWord("resilient", 5),
		vocabWord("ubiquitous", 9), // not in this chunk
	}
	tokens := []model.Token{tok(0, "resilience", "resilient"), tok(1, "matters")}

	got := enrich.NarrowKnownTerms(entries, "ru", tokens)

	if !slices.Equal(got, []string{"resilient"}) {
		t.Errorf("narrowed list = %v, want [resilient] — only terms in this chunk", got)
	}
}

func TestNarrowKnownTermsMatchesInflectedOccurrences(t *testing.T) {
	// A surface-form intersection would have missed this entirely; the lemma
	// makes it exact.
	entries := []model.VocabEntry{vocabWord("run", 3)}
	tokens := []model.Token{tok(0, "ran", "run")}

	if got := enrich.NarrowKnownTerms(entries, "ru", tokens); !slices.Equal(got, []string{"run"}) {
		t.Errorf("narrowed list = %v, want [run]", got)
	}
}

func TestNarrowKnownTermsRanksByCountAndCaps(t *testing.T) {
	var entries []model.VocabEntry
	var tokens []model.Token
	// More entries than the cap, with ascending counts.
	for i := range enrich.MaxKnownTermsInPrompt + 50 {
		lemma := fmt.Sprintf("term%03d", i)
		entries = append(entries, vocabWord(lemma, i))
		tokens = append(tokens, tok(i, lemma))
	}

	got := enrich.NarrowKnownTerms(entries, "ru", tokens)

	if len(got) != enrich.MaxKnownTermsInPrompt {
		t.Fatalf("len = %d, want the cap %d", len(got), enrich.MaxKnownTermsInPrompt)
	}
	// Most-established first: the highest count leads.
	want := fmt.Sprintf("term%03d", enrich.MaxKnownTermsInPrompt+49)
	if got[0] != want {
		t.Errorf("got[0] = %q, want %q (highest count wins the slots)", got[0], want)
	}
}

func TestNarrowKnownTermsIsEmptyWithoutVocabulary(t *testing.T) {
	if got := enrich.NarrowKnownTerms(nil, "ru", []model.Token{tok(0, "word")}); len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

// ── End-to-end through the pool ───────────────────────────────────────────────

// enrichedArticle seeds a fetched article whose tokens carry lemmas.
func enrichedArticle(id string) *model.Article {
	return &model.Article{
		ID:           id,
		Status:       model.StatusFetched,
		Title:        "T",
		OriginalText: "The resilience of markets matters",
		Tokens: []model.Token{
			tok(0, "The"), tok(1, "resilience", "resilient"), tok(2, "of"),
			tok(3, "markets", "market"), tok(4, "matters"),
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

func TestPoolPassesNarrowedKnownTermsAndFiltersTheAnswer(t *testing.T) {
	st := newFakeStore()
	st.settings = model.Settings{
		TargetLanguage: "ru", CEFRLevel: model.CEFRB1,
		MinDifficultyToHighlight: model.CEFRB2, VocabAssist: true,
	}
	st.knownVocab = []model.VocabEntry{vocabWord("resilient", 4), vocabWord("ubiquitous", 7)}
	a := enrichedArticle("a1")
	st.articles[a.ID] = a

	// The model ignores the instruction and annotates the known word anyway.
	llm := &fakeLLM{result: &model.Enrichment{
		DifficultWords: []model.DifficultWord{
			{TokenIndex: 1, Lemma: "resilience", Translation: "устойчивость", CEFRLevel: model.CEFRB2},
			{TokenIndex: 3, Lemma: "market", Translation: "рынок", CEFRLevel: model.CEFRB2},
		},
		Sentences: []model.Sentence{{StartIndex: 0, EndIndex: 4, Translation: "Устойчивость рынков важна."}},
	}}
	llm.spanResult = llm.result

	pool := enrich.NewPool(testCfg(1, 1), st, &fakeExtractor{}, llm, nil)
	if !runPool(t, pool, 3*time.Second, func() bool { return st.status(a.ID) == model.StatusEnriched }) {
		t.Fatalf("article not enriched, status=%q", st.status(a.ID))
	}

	// The prompt carried only the term present in this chunk.
	opts := llm.lastEnrichOptions()
	if !slices.Equal(opts.KnownTerms, []string{"resilient"}) {
		t.Errorf("prompt known terms = %v, want [resilient]", opts.KnownTerms)
	}

	// …and the answer was filtered deterministically regardless.
	got, _ := st.savedEnrichment(a.ID)
	if len(got.DifficultWords) != 1 || got.DifficultWords[0].TokenIndex != 3 {
		t.Errorf("post-filter did not drop the known word: %+v", got.DifficultWords)
	}
	if len(got.Sentences) != 1 {
		t.Errorf("sentence translations were affected: %+v", got.Sentences)
	}
}

func TestPoolVocabAssistOffDisablesBothHalves(t *testing.T) {
	st := newFakeStore()
	st.settings = model.Settings{
		TargetLanguage: "ru", CEFRLevel: model.CEFRB1,
		MinDifficultyToHighlight: model.CEFRB2, VocabAssist: false,
	}
	st.knownVocab = []model.VocabEntry{vocabWord("resilient", 4)}
	a := enrichedArticle("a1")
	st.articles[a.ID] = a

	llm := &fakeLLM{result: &model.Enrichment{
		DifficultWords: []model.DifficultWord{
			{TokenIndex: 1, Lemma: "resilience", Translation: "устойчивость", CEFRLevel: model.CEFRB2},
		},
	}}
	llm.spanResult = llm.result

	pool := enrich.NewPool(testCfg(1, 1), st, &fakeExtractor{}, llm, nil)
	if !runPool(t, pool, 3*time.Second, func() bool { return st.status(a.ID) == model.StatusEnriched }) {
		t.Fatalf("article not enriched, status=%q", st.status(a.ID))
	}

	if terms := llm.lastEnrichOptions().KnownTerms; len(terms) != 0 {
		t.Errorf("prompt carried known terms with vocab_assist off: %v", terms)
	}
	if got, _ := st.savedEnrichment(a.ID); len(got.DifficultWords) != 1 {
		t.Errorf("the post-filter ran with vocab_assist off: %+v", got.DifficultWords)
	}
}

func TestPoolSurvivesAVocabularyReadFailure(t *testing.T) {
	st := newFakeStore()
	st.settings = model.Settings{
		TargetLanguage: "ru", CEFRLevel: model.CEFRB1,
		MinDifficultyToHighlight: model.CEFRB2, VocabAssist: true,
	}
	st.knownVocabErr = fmt.Errorf("database is locked")
	a := enrichedArticle("a1")
	st.articles[a.ID] = a

	llm := &fakeLLM{result: &model.Enrichment{
		DifficultWords: []model.DifficultWord{
			{TokenIndex: 1, Lemma: "resilience", Translation: "устойчивость", CEFRLevel: model.CEFRB2},
		},
	}}
	llm.spanResult = llm.result

	pool := enrich.NewPool(testCfg(1, 1), st, &fakeExtractor{}, llm, nil)
	// Degrades to "annotate everything" rather than failing the article.
	if !runPool(t, pool, 3*time.Second, func() bool { return st.status(a.ID) == model.StatusEnriched }) {
		t.Fatalf("a vocabulary read failure blocked enrichment, status=%q", st.status(a.ID))
	}
	if got, _ := st.savedEnrichment(a.ID); len(got.DifficultWords) != 1 {
		t.Errorf("annotations lost after a vocabulary read failure: %+v", got.DifficultWords)
	}
}
