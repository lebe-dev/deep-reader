package api

import (
	"slices"

	"deep-reader/internal/model"
)

// validateSettingsPatch checks the supplied partial settings against the legal
// domain values. It returns a human-readable message and false on the first
// invalid field; ok is true when every present field is valid (nil fields are
// skipped — they leave the existing value unchanged).
func validateSettingsPatch(patch model.SettingsPatch) (msg string, ok bool) {
	if patch.CEFRLevel != nil && !slices.Contains(model.CEFRLevels, *patch.CEFRLevel) {
		return "cefr_level must be one of A2, B1, B2, C1, C2", false
	}
	if patch.MinDifficultyToHighlight != nil && !slices.Contains(model.CEFRLevels, *patch.MinDifficultyToHighlight) {
		return "min_difficulty_to_highlight must be one of A2, B1, B2, C1, C2", false
	}
	if patch.TargetLanguage != nil && *patch.TargetLanguage == "" {
		return "target_language must not be empty", false
	}
	if patch.LLMModel != nil && *patch.LLMModel == "" {
		return "llm_model must not be empty", false
	}
	if patch.MarkdownWarnThreshold != nil && (*patch.MarkdownWarnThreshold < 0 || *patch.MarkdownWarnThreshold > model.MaxMarkdownWarnThreshold) {
		return "markdown_warn_threshold must be between 0 and 100", false
	}
	// enrichment_prompt may be empty (= use the built-in default); only the
	// upper length bound is enforced.
	if patch.EnrichmentPrompt != nil && len(*patch.EnrichmentPrompt) > model.MaxEnrichmentPromptLen {
		return "enrichment_prompt is too long", false
	}
	// summary_prompt may be empty (= use the built-in default); only the upper
	// length bound is enforced.
	if patch.SummaryPrompt != nil && len(*patch.SummaryPrompt) > model.MaxEnrichmentPromptLen {
		return "summary_prompt is too long", false
	}
	// normalize_prompt may be empty (= use the built-in default); only the upper
	// length bound is enforced.
	if patch.NormalizePrompt != nil && len(*patch.NormalizePrompt) > model.MaxEnrichmentPromptLen {
		return "normalize_prompt is too long", false
	}
	// bot_wall_signatures may be empty (= use the built-in defaults); only the
	// upper length bound is enforced.
	if patch.BotWallSignatures != nil && len(*patch.BotWallSignatures) > model.MaxBotWallSignaturesLen {
		return "bot_wall_signatures is too long", false
	}
	// chunk_tokens: 0 means "use the deployment default"; any other value must
	// fall within the supported window-size range.
	if patch.ChunkTokens != nil && *patch.ChunkTokens != 0 &&
		(*patch.ChunkTokens < model.MinChunkTokens || *patch.ChunkTokens > model.MaxChunkTokens) {
		return "chunk_tokens must be 0 (default) or between 50 and 2000", false
	}
	return "", true
}
