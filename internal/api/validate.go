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
	return "", true
}
