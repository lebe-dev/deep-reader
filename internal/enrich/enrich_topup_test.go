package enrich_test

import (
	"reflect"
	"testing"
	"time"

	"deep-reader/internal/enrich"
	"deep-reader/internal/model"
)

func sentence(start, end int) model.Sentence {
	return model.Sentence{StartIndex: start, EndIndex: end, Translation: "перевод"}
}

func word(idx int) model.DifficultWord {
	return model.DifficultWord{TokenIndex: idx, Lemma: "word", Translation: "слово", CEFRLevel: model.CEFRB2}
}

// TestUncoveredSpans checks the gap computation that drives incremental enrich.
func TestUncoveredSpans(t *testing.T) {
	tests := []struct {
		name       string
		sentences  []model.Sentence
		tokenCount int
		want       []model.Span
	}{
		{
			name:       "fully covered",
			sentences:  []model.Sentence{sentence(0, 4)},
			tokenCount: 5,
			want:       nil,
		},
		{
			name:       "no sentences at all",
			sentences:  nil,
			tokenCount: 3,
			want:       []model.Span{{Start: 0, End: 2}},
		},
		{
			name:       "gap at the tail",
			sentences:  []model.Sentence{sentence(0, 2)},
			tokenCount: 6,
			want:       []model.Span{{Start: 3, End: 5}},
		},
		{
			name:       "gap in the middle",
			sentences:  []model.Sentence{sentence(0, 1), sentence(5, 7)},
			tokenCount: 8,
			want:       []model.Span{{Start: 2, End: 4}},
		},
		{
			name:       "gap at the head and middle",
			sentences:  []model.Sentence{sentence(3, 4)},
			tokenCount: 8,
			want:       []model.Span{{Start: 0, End: 2}, {Start: 5, End: 7}},
		},
		{
			name:       "zero tokens",
			sentences:  nil,
			tokenCount: 0,
			want:       nil,
		},
		{
			name:       "overlapping sentences still fully cover",
			sentences:  []model.Sentence{sentence(0, 3), sentence(2, 5)},
			tokenCount: 6,
			want:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := enrich.UncoveredSpans(model.Enrichment{Sentences: tt.sentences}, tt.tokenCount)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("uncoveredSpans = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestMergeEnrichment verifies additive, idempotent merging with dedup and
// non-overlapping sentence preservation.
func TestMergeEnrichment(t *testing.T) {
	existing := model.Enrichment{
		DifficultWords: []model.DifficultWord{word(0)},
		Phrases:        []model.Phrase{{StartIndex: 0, EndIndex: 1, Type: model.PhraseTypeIdiom, Text: "word word", Translation: "x"}},
		Sentences:      []model.Sentence{sentence(0, 2)},
		Glossary:       []model.GlossaryItem{{Term: "alpha", Definition: "a"}},
	}
	addition := model.Enrichment{
		// word(0) is a duplicate index → dropped; word(4) is new.
		DifficultWords: []model.DifficultWord{word(0), word(4)},
		// same (0,1) range → dropped; (3,4) is new.
		Phrases: []model.Phrase{
			{StartIndex: 0, EndIndex: 1, Type: model.PhraseTypeIdiom, Text: "word word", Translation: "dup"},
			{StartIndex: 3, EndIndex: 4, Type: model.PhraseTypeTerm, Text: "word word", Translation: "new"},
		},
		// sentence (1,3) overlaps existing (0,2) → dropped; (3,5) is clean → kept.
		Sentences: []model.Sentence{sentence(1, 3), sentence(3, 5)},
		// duplicate term alpha → dropped; beta new.
		Glossary: []model.GlossaryItem{{Term: "alpha", Definition: "dup"}, {Term: "beta", Definition: "b"}},
	}

	got := enrich.MergeEnrichment(existing, addition)

	if len(got.DifficultWords) != 2 {
		t.Errorf("difficult words = %d, want 2", len(got.DifficultWords))
	}
	if len(got.Phrases) != 2 {
		t.Errorf("phrases = %d, want 2", len(got.Phrases))
	}
	if len(got.Sentences) != 2 {
		t.Errorf("sentences = %d, want 2 (overlapping addition dropped)", len(got.Sentences))
	}
	if len(got.Glossary) != 2 {
		t.Errorf("glossary = %d, want 2", len(got.Glossary))
	}

	// Idempotency: merging the result with the same addition changes nothing.
	again := enrich.MergeEnrichment(got, addition)
	if !reflect.DeepEqual(again, got) {
		t.Errorf("merge not idempotent:\n got=%+v\nagain=%+v", got, again)
	}

	// Existing input must not be mutated.
	if len(existing.Sentences) != 1 {
		t.Errorf("existing was mutated: sentences=%d", len(existing.Sentences))
	}
}

// TestTopUpFillsGap drives the worker through an incremental enrich: an
// already-enriched article missing the tail is topped up and the merged
// enrichment covers the full token range.
func TestTopUpFillsGap(t *testing.T) {
	const id = "topup-1"
	article := makeArticle(id, 6)
	article.Status = model.StatusTopupQueued
	st := newFakeStore(article)
	// Existing enrichment covers only [0,2]; tokens [3,5] are an uncovered gap.
	st.enrichments[id] = model.Enrichment{
		DifficultWords: []model.DifficultWord{word(0)},
		Sentences:      []model.Sentence{sentence(0, 2)},
	}

	llm := &fakeLLM{
		spanResult: &model.Enrichment{
			DifficultWords: []model.DifficultWord{word(4)},
			Sentences:      []model.Sentence{sentence(3, 5)},
		},
	}
	pool := enrich.NewPool(testCfg(1, 3), st, &fakeExtractor{}, llm)

	ok := runPool(t, pool, 3*time.Second, func() bool {
		return st.status(id) == model.StatusEnriched
	})
	if !ok {
		t.Fatalf("expected status=enriched, got %q", st.status(id))
	}

	if llm.spanCallCount() != 1 {
		t.Errorf("expected 1 EnrichSpans call, got %d", llm.spanCallCount())
	}
	if want := []model.Span{{Start: 3, End: 5}}; !reflect.DeepEqual(llm.lastSpans, want) {
		t.Errorf("EnrichSpans spans = %+v, want %+v", llm.lastSpans, want)
	}

	merged, saved := st.savedEnrichment(id)
	if !saved {
		t.Fatal("expected merged enrichment to be saved")
	}
	if len(merged.Sentences) != 2 {
		t.Errorf("merged sentences = %d, want 2", len(merged.Sentences))
	}
	if len(merged.DifficultWords) != 2 {
		t.Errorf("merged difficult words = %d, want 2", len(merged.DifficultWords))
	}
}

// TestTopUpNoGaps verifies that an already fully-covered article is restored to
// enriched without any LLM call.
func TestTopUpNoGaps(t *testing.T) {
	const id = "topup-2"
	article := makeArticle(id, 4)
	article.Status = model.StatusTopupQueued
	st := newFakeStore(article)
	st.enrichments[id] = model.Enrichment{
		Sentences: []model.Sentence{sentence(0, 3)},
	}

	llm := &fakeLLM{}
	pool := enrich.NewPool(testCfg(1, 3), st, &fakeExtractor{}, llm)

	ok := runPool(t, pool, 3*time.Second, func() bool {
		return st.status(id) == model.StatusEnriched
	})
	if !ok {
		t.Fatalf("expected status=enriched, got %q", st.status(id))
	}
	if llm.spanCallCount() != 0 {
		t.Errorf("expected no EnrichSpans calls when fully covered, got %d", llm.spanCallCount())
	}
}
