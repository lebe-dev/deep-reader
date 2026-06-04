package normalize

import (
	"strings"
	"testing"

	"deep-reader/internal/model"
)

func TestRenderSystemPrompt(t *testing.T) {
	t.Run("default when unset, substitutes target language", func(t *testing.T) {
		got := RenderSystemPrompt(model.Settings{TargetLanguage: "ru"})
		if !strings.Contains(got, "ru") {
			t.Fatalf("expected target language substituted, got: %q", got)
		}
		if strings.Contains(got, "{{target_language}}") {
			t.Fatalf("placeholder left unsubstituted: %q", got)
		}
		if !strings.Contains(got, "content-cleaning") {
			t.Fatalf("expected default template, got: %q", got)
		}
	})

	t.Run("custom template wins and is still rendered", func(t *testing.T) {
		got := RenderSystemPrompt(model.Settings{
			TargetLanguage:  "de",
			NormalizePrompt: "Clean this for {{target_language}} learners.",
		})
		if got != "Clean this for de learners." {
			t.Fatalf("unexpected render: %q", got)
		}
	})
}

func TestApply(t *testing.T) {
	// A realistic article body; the cleaned variants below are measured against it.
	const original = "The quick brown fox jumps over the lazy dog. " +
		"Pack my box with five dozen liquor jugs. " +
		"How vexingly quick daft zebras jump."

	t.Run("accepts a reasonable cleanup", func(t *testing.T) {
		// Drops one trailing sentence (well under half the words).
		cleaned := "The quick brown fox jumps over the lazy dog. " +
			"Pack my box with five dozen liquor jugs."
		got, ok := Apply(original, cleaned)
		if !ok || got != cleaned {
			t.Fatalf("expected cleaned accepted; ok=%v got=%q", ok, got)
		}
	})

	t.Run("rejects empty result, keeps original", func(t *testing.T) {
		got, ok := Apply(original, "   \n  ")
		if ok || got != original {
			t.Fatalf("expected original kept on empty cleaned; ok=%v got=%q", ok, got)
		}
	})

	t.Run("rejects over-deletion below keep ratio", func(t *testing.T) {
		// Only a few words survive — far below MinKeepRatio.
		got, ok := Apply(original, "The quick brown fox.")
		if ok || got != original {
			t.Fatalf("expected original kept on over-deletion; ok=%v got=%q", ok, got)
		}
	})

	t.Run("trims accepted result", func(t *testing.T) {
		cleaned := "  " + original + "  "
		got, ok := Apply(original, cleaned)
		if !ok || got != original {
			t.Fatalf("expected trimmed accepted result; ok=%v got=%q", ok, got)
		}
	})

	t.Run("empty original accepts any non-empty cleaned", func(t *testing.T) {
		got, ok := Apply("", "anything")
		if !ok || got != "anything" {
			t.Fatalf("expected cleaned accepted when original empty; ok=%v got=%q", ok, got)
		}
	})
}

func TestWordCount(t *testing.T) {
	cases := map[string]int{
		"":                      0,
		"   ":                   0,
		"--- *** ___":           0,
		"hello":                 1,
		"hello world":           2,
		"snake_case stays one":  3,
		"  spaced   out  here ": 3,
		"90 Comments":           2,
	}
	for in, want := range cases {
		if got := wordCount(in); got != want {
			t.Errorf("wordCount(%q) = %d, want %d", in, got, want)
		}
	}
}
