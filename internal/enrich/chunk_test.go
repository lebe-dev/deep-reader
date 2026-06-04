package enrich_test

import (
	"reflect"
	"testing"
	"time"

	"deep-reader/internal/enrich"
	"deep-reader/internal/model"
	"deep-reader/internal/tokenize"
)

// spans tokenizes text and chunks it, returning the inclusive index ranges.
func chunk(t *testing.T, text string, maxTokens int) ([]model.Token, []model.Span) {
	t.Helper()
	tokens := tokenize.Tokenize(text)
	return tokens, enrich.ChunkSpans(text, tokens, maxTokens)
}

func TestChunkSpans_Empty(t *testing.T) {
	if got := enrich.ChunkSpans("", nil, 10); got != nil {
		t.Fatalf("expected nil for no tokens, got %v", got)
	}
}

func TestChunkSpans_SingleChunkWhenShort(t *testing.T) {
	text := "The quick brown fox jumps."
	tokens, spans := chunk(t, text, 100)
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d: %v", len(spans), spans)
	}
	if spans[0] != (model.Span{Start: 0, End: len(tokens) - 1}) {
		t.Fatalf("expected full span, got %v (token count=%d)", spans[0], len(tokens))
	}
}

// Chunks must be contiguous, non-overlapping, and cover every token exactly once.
func TestChunkSpans_CoversAllTokensContiguously(t *testing.T) {
	text := "Alpha beta gamma. Delta epsilon zeta. Eta theta iota. " +
		"Kappa lambda mu. Nu xi omicron. Pi rho sigma. Tau upsilon phi."
	tokens, spans := chunk(t, text, 5)
	if len(spans) < 2 {
		t.Fatalf("expected multiple chunks, got %d: %v", len(spans), spans)
	}
	want := 0
	for i, s := range spans {
		if s.Start != want {
			t.Fatalf("span %d starts at %d, want %d (gap/overlap): %v", i, s.Start, want, spans)
		}
		if s.End < s.Start {
			t.Fatalf("span %d is empty/inverted: %v", i, s)
		}
		want = s.End + 1
	}
	if want != len(tokens) {
		t.Fatalf("spans cover %d tokens, want %d", want, len(tokens))
	}
}

// A chunk boundary must fall on a sentence end (a '.', '!' or '?' between the
// last token of one chunk and the first token of the next), not mid-sentence,
// as long as a boundary exists within the hard cap.
func TestChunkSpans_SnapsToSentenceBoundary(t *testing.T) {
	// Six 3-word sentences; with maxTokens=4 each chunk should close at the end
	// of a sentence rather than after exactly 4 words.
	text := "one two three. four five six. seven eight nine. ten eleven twelve."
	tokens, spans := chunk(t, text, 4)
	for _, s := range spans {
		if s.End == len(tokens)-1 {
			continue // last chunk ends at end of text
		}
		gap := text[tokens[s.End].End:tokens[s.End+1].Start]
		if !containsSentenceEnd(gap) {
			t.Fatalf("chunk ending at token %d (%q) is not a sentence boundary; gap=%q; spans=%v",
				s.End, tokens[s.End].Text, gap, spans)
		}
	}
}

// When no sentence boundary appears for a long stretch, the chunker must still
// cut at the hard cap rather than emit one giant chunk.
func TestChunkSpans_HardCapWithoutBoundary(t *testing.T) {
	// 20 words with no sentence-ending punctuation at all.
	text := "a b c d e f g h i j k l m n o p q r s t"
	tokens, spans := chunk(t, text, 4)
	if len(spans) < 2 {
		t.Fatalf("expected the hard cap to force multiple chunks, got %d: %v", len(spans), spans)
	}
	hardCap := 4 + 4/2
	for _, s := range spans {
		if got := s.End - s.Start + 1; got > hardCap {
			t.Fatalf("chunk %v has %d tokens, exceeds hard cap %d", s, got, hardCap)
		}
	}
	_ = tokens
}

func TestChunkSpans_ResultMatchesFullCoverage(t *testing.T) {
	text := "First sentence here. Second one follows. Third and final."
	_, spans := chunk(t, text, 3)
	// Sanity: reconstructed coverage equals contiguous [0,n).
	var flat []int
	for _, s := range spans {
		for i := s.Start; i <= s.End; i++ {
			flat = append(flat, i)
		}
	}
	want := make([]int, len(flat))
	for i := range want {
		want[i] = i
	}
	if !reflect.DeepEqual(flat, want) {
		t.Fatalf("flattened spans %v != %v", flat, want)
	}
}

// TestStepwiseEnrichmentChunks drives a multi-chunk article through the pool and
// verifies that the enrichment runs step-wise: a summary is produced once, each
// chunk is annotated by its own EnrichSpans call, and the merged result ends
// fully covered with the article flipped to enriched.
func TestStepwiseEnrichmentChunks(t *testing.T) {
	text := "one two three. four five six. seven eight nine. " +
		"ten eleven twelve. apple banana cherry. delta echo foxtrot."
	tokens := tokenize.Tokenize(text)

	article := &model.Article{
		ID:           "art-chunk",
		Title:        "Chunked",
		Status:       model.StatusFetched,
		OriginalText: text,
		Tokens:       tokens,
	}
	st := newFakeStore(article)
	llm := &fakeLLM{
		spanFunc: func(spans []model.Span) *model.Enrichment {
			e := &model.Enrichment{}
			for _, s := range spans {
				e.Sentences = append(e.Sentences, model.Sentence{
					StartIndex: s.Start, EndIndex: s.End, Translation: "перевод",
				})
			}
			return e
		},
	}

	cfg := testCfg(1, 1)
	cfg.LLMChunkTokens = 4 // force several chunks for the ~18-token article
	pool := enrich.NewPool(cfg, st, &fakeExtractor{}, llm)

	ok := runPool(t, pool, 5*time.Second, func() bool {
		return st.status("art-chunk") == model.StatusEnriched
	})
	if !ok {
		t.Fatalf("expected status=enriched, got %q", st.status("art-chunk"))
	}

	if llm.spanCallCount() < 2 {
		t.Fatalf("expected multiple per-chunk EnrichSpans calls, got %d", llm.spanCallCount())
	}
	if llm.summaryCalls != 1 {
		t.Fatalf("expected exactly one Summarize call, got %d", llm.summaryCalls)
	}

	e, saved := st.savedEnrichment("art-chunk")
	if !saved {
		t.Fatal("expected enrichment to be saved")
	}
	// Every token must be covered by some sentence (full coverage across chunks).
	for i := range tokens {
		covered := false
		for _, s := range e.Sentences {
			if i >= s.StartIndex && i <= s.EndIndex {
				covered = true
				break
			}
		}
		if !covered {
			t.Fatalf("token %d left uncovered after step-wise enrichment: %+v", i, e.Sentences)
		}
	}
}

// TestChunkTokensSettingOverridesConfig verifies that Settings.ChunkTokens, when
// set, drives the per-chunk window size instead of the deployment default: a
// small override forces several chunks for an article the config default would
// annotate in a single call.
func TestChunkTokensSettingOverridesConfig(t *testing.T) {
	text := "one two three. four five six. seven eight nine. " +
		"ten eleven twelve. apple banana cherry. delta echo foxtrot."
	tokens := tokenize.Tokenize(text)

	article := &model.Article{
		ID:           "art-chunktokens",
		Title:        "Chunked",
		Status:       model.StatusFetched,
		OriginalText: text,
		Tokens:       tokens,
	}
	st := newFakeStore(article)
	// Override the per-user chunk size to a small value; the config default
	// (500, set by testCfg) would annotate this ~18-token article in one chunk.
	st.settings.ChunkTokens = 4

	llm := &fakeLLM{
		spanFunc: func(spans []model.Span) *model.Enrichment {
			e := &model.Enrichment{}
			for _, s := range spans {
				e.Sentences = append(e.Sentences, model.Sentence{
					StartIndex: s.Start, EndIndex: s.End, Translation: "перевод",
				})
			}
			return e
		},
	}

	cfg := testCfg(1, 1) // cfg.LLMChunkTokens = 500
	pool := enrich.NewPool(cfg, st, &fakeExtractor{}, llm)

	ok := runPool(t, pool, 5*time.Second, func() bool {
		return st.status("art-chunktokens") == model.StatusEnriched
	})
	if !ok {
		t.Fatalf("expected status=enriched, got %q", st.status("art-chunktokens"))
	}
	if llm.spanCallCount() < 2 {
		t.Fatalf("expected the small ChunkTokens override to force several chunks, got %d calls", llm.spanCallCount())
	}
}

func containsSentenceEnd(gap string) bool {
	for _, r := range gap {
		if r == '.' || r == '!' || r == '?' {
			return true
		}
	}
	return false
}
