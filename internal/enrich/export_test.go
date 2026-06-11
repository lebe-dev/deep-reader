// export_test.go exposes private helpers for whitebox testing.
package enrich

import (
	"log/slog"
	"time"

	"deep-reader/internal/model"
	"deep-reader/internal/ports"
)

// BackoffDuration exposes the unexported backoffDuration for unit tests.
func BackoffDuration(attempt int, base, cap time.Duration) time.Duration {
	return backoffDuration(attempt, base, cap)
}

// IsRetryable exposes the unexported isRetryable for unit tests.
func IsRetryable(err error) bool {
	return isRetryable(err)
}

// UncoveredSpans exposes the unexported uncoveredSpans for unit tests.
func UncoveredSpans(e model.Enrichment, tokenCount int) []model.Span {
	return uncoveredSpans(e, tokenCount)
}

// MergeEnrichment exposes the unexported mergeEnrichment for unit tests.
func MergeEnrichment(existing, addition model.Enrichment) model.Enrichment {
	return mergeEnrichment(existing, addition)
}

// ChunkSpans exposes the unexported chunkSpans for unit tests.
func ChunkSpans(text string, tokens []model.Token, maxTokens int) []model.Span {
	return chunkSpans(text, tokens, maxTokens)
}

// ExpandSpansToSentences exposes the unexported expandSpansToSentences for unit tests.
func ExpandSpansToSentences(text string, tokens []model.Token, spans []model.Span) []model.Span {
	return expandSpansToSentences(text, tokens, spans)
}

// SanitizeEnrichment exposes the unexported sanitizeEnrichment for unit tests.
func SanitizeEnrichment(e model.Enrichment, tokens []model.Token) model.Enrichment {
	return sanitizeEnrichment(e, tokens, slog.Default())
}

// DetectBotWall exposes the unexported detectBotWall for unit tests.
func DetectBotWall(result *ports.ExtractResult, signatures []string) string {
	return detectBotWall(result, signatures)
}

// BotWallSignaturesFor exposes the unexported botWallSignaturesFor for unit tests.
func BotWallSignaturesFor(settings model.Settings) []string {
	return botWallSignaturesFor(settings)
}

// BotWallMaxWordsForTest exposes the unexported botWallMaxWords length guard.
func BotWallMaxWordsForTest() int {
	return botWallMaxWords
}
