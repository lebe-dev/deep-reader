package enrich

import (
	"sort"
	"strings"

	"deep-reader/internal/model"
)

// chunkSpans splits the article's full token range [0, len(tokens)) into
// contiguous, non-overlapping inclusive spans of roughly maxTokens tokens each,
// snapping every boundary to the end of a sentence so that no sentence is split
// across two chunks. This is the unit of work for the step-wise enrichment: each
// span is annotated by its own bounded LLM call (Client.EnrichSpans) and merged
// into the article's enrichment, which keeps every individual completion short
// enough to avoid the truncated-JSON failure that a single whole-article call
// hits on long articles.
//
// Boundary detection uses the byte gap between consecutive tokens: the
// tokenizer drops pure-punctuation characters but preserves exact byte offsets,
// so a sentence-ending '.', '!' or '?' in text[tokens[i].End:tokens[i+1].Start]
// marks token i as a sentence-final token (see package tokenize).
//
// To bound the worst case where a stretch carries no sentence punctuation (code
// blocks, lists), a chunk is force-closed once it reaches a hard cap of
// 1.5×maxTokens even without a boundary. The returned spans always cover every
// token exactly once and in order.
func chunkSpans(text string, tokens []model.Token, maxTokens int) []model.Span {
	n := len(tokens)
	if n == 0 {
		return nil
	}
	if maxTokens < 1 {
		maxTokens = 1
	}
	hardCap := maxTokens + maxTokens/2

	var spans []model.Span
	start := 0
	for start < n {
		end := start
		for {
			length := end - start + 1
			last := end == n-1
			if last || (length >= maxTokens && isSentenceEnd(text, tokens, end)) || length >= hardCap {
				break
			}
			end++
		}
		spans = append(spans, model.Span{Start: start, End: end})
		start = end + 1
	}
	return spans
}

// isSentenceEnd reports whether token i is the last token of a sentence: either
// the final token of the article, or followed by a byte gap that contains a
// sentence-terminating rune.
func isSentenceEnd(text string, tokens []model.Token, i int) bool {
	if i >= len(tokens)-1 {
		return true
	}
	gap := text[tokens[i].End:tokens[i+1].Start]
	return strings.ContainsAny(gap, ".!?")
}

// expandSpansToSentences widens each span outward to the enclosing sentence
// boundaries and coalesces the result into ascending, non-overlapping spans.
//
// The incremental ("top up") enrich sends only the tokens inside the requested
// spans (see llm.buildSpanPrompt). The uncovered gaps it starts from
// (uncoveredSpans) can begin or end mid-sentence, so without this the model
// would be asked to translate a sentence it can only partially see. Expanding
// every gap to whole sentences — the same boundary rule chunkSpans uses —
// guarantees each span the model receives contains only complete sentences.
func expandSpansToSentences(text string, tokens []model.Token, spans []model.Span) []model.Span {
	n := len(tokens)
	// Without the source text sentence boundaries cannot be detected (isSentenceEnd
	// reads the byte gap between tokens), so fall back to the raw spans unchanged.
	if n == 0 || len(spans) == 0 || text == "" {
		return spans
	}
	expanded := make([]model.Span, 0, len(spans))
	for _, s := range spans {
		start := s.Start
		for start > 0 && !isSentenceEnd(text, tokens, start-1) {
			start--
		}
		end := s.End
		for end < n-1 && !isSentenceEnd(text, tokens, end) {
			end++
		}
		expanded = append(expanded, model.Span{Start: start, End: end})
	}
	return coalesceSpans(expanded)
}

// coalesceSpans sorts spans ascending by start and merges every overlapping or
// adjacent pair into a minimal set of non-overlapping spans. It mutates and
// reuses the input slice's backing array.
func coalesceSpans(spans []model.Span) []model.Span {
	if len(spans) <= 1 {
		return spans
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })
	out := spans[:1]
	for _, s := range spans[1:] {
		last := &out[len(out)-1]
		if s.Start <= last.End+1 {
			if s.End > last.End {
				last.End = s.End
			}
			continue
		}
		out = append(out, s)
	}
	return out
}
