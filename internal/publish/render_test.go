package publish

import (
	"testing"

	"deep-reader/internal/model"
	"deep-reader/internal/tokenize"
)

// payload builds an ArticlePayload from source text plus sentence translations
// expressed as token ranges, so tests read as "these tokens carry this
// translation" rather than as byte arithmetic.
func payload(text string, sentences ...model.Sentence) *model.ArticlePayload {
	return &model.ArticlePayload{
		OriginalText: text,
		Tokens:       tokenize.Tokenize(text),
		Enrichment:   &model.Enrichment{Sentences: sentences},
	}
}

// flatten renders blocks as "translated|original" segment lists per paragraph,
// which is compact enough to assert on directly.
func flatten(blocks []Block) []string {
	out := make([]string, 0, len(blocks))
	for _, b := range blocks {
		parts := make([]string, 0, len(b.Segments))
		for _, s := range b.Segments {
			kind := "orig"
			if s.Translated {
				kind = "ru"
			}
			parts = append(parts, kind+":"+s.Text)
		}
		out = append(out, joinWith(parts, " / "))
	}
	return out
}

func joinWith(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

func assertBlocks(t *testing.T, got []Block, want []string) {
	t.Helper()
	flat := flatten(got)
	if len(flat) != len(want) {
		t.Fatalf("block count = %d (%v), want %d (%v)", len(flat), flat, len(want), want)
	}
	for i := range want {
		if flat[i] != want[i] {
			t.Errorf("block %d = %q, want %q", i, flat[i], want[i])
		}
	}
}

func TestBlocksRendersSentenceTranslationsInSourceOrder(t *testing.T) {
	text := "First one. Second one."
	toks := tokenize.Tokenize(text)
	// "Second one." is the tail of the token stream; "First one." the head.
	mid := len(toks) / 2

	// Deliberately out of order: the renderer must sort by source position.
	p := payload(text,
		model.Sentence{StartIndex: mid, EndIndex: len(toks) - 1, Translation: "Второе."},
		model.Sentence{StartIndex: 0, EndIndex: mid - 1, Translation: "Первое."},
	)

	assertBlocks(t, Blocks(p), []string{"ru:Первое. / ru:Второе."})
}

func TestBlocksSplitsParagraphsOnBlankLines(t *testing.T) {
	text := "First one.\n\nSecond one."
	toks := tokenize.Tokenize(text)
	mid := len(toks) / 2

	p := payload(text,
		model.Sentence{StartIndex: 0, EndIndex: mid - 1, Translation: "Первое."},
		model.Sentence{StartIndex: mid, EndIndex: len(toks) - 1, Translation: "Второе."},
	)

	assertBlocks(t, Blocks(p), []string{"ru:Первое.", "ru:Второе."})
}

func TestBlocksKeepsUncoveredTextInTheOriginal(t *testing.T) {
	// Only the first sentence is translated; the second must survive verbatim
	// rather than vanish from the published page.
	text := "First one. Second one."
	toks := tokenize.Tokenize(text)
	mid := len(toks) / 2

	p := payload(text, model.Sentence{StartIndex: 0, EndIndex: mid - 1, Translation: "Первое."})

	assertBlocks(t, Blocks(p), []string{"ru:Первое. / orig:Second one."})
}

func TestBlocksKeepsTextBeforeTheFirstTranslation(t *testing.T) {
	text := "Untranslated lead. Translated tail."
	toks := tokenize.Tokenize(text)
	mid := len(toks) / 2

	p := payload(text, model.Sentence{StartIndex: mid, EndIndex: len(toks) - 1, Translation: "Переведённый хвост."})

	assertBlocks(t, Blocks(p), []string{"orig:Untranslated lead. / ru:Переведённый хвост."})
}

func TestBlocksDropsOverlappingAndInvalidSpans(t *testing.T) {
	text := "Only sentence here."
	toks := tokenize.Tokenize(text)
	last := len(toks) - 1

	p := payload(text,
		model.Sentence{StartIndex: 0, EndIndex: last, Translation: "Единственное предложение."},
		// Overlaps the span above — including it would print the text twice.
		model.Sentence{StartIndex: 0, EndIndex: last, Translation: "Дубликат."},
		// Out of range: the enrichment references a token that does not exist.
		model.Sentence{StartIndex: last + 5, EndIndex: last + 9, Translation: "Мусор."},
		// Inverted span.
		model.Sentence{StartIndex: 2, EndIndex: 1, Translation: "Инверсия."},
		// Empty translation.
		model.Sentence{StartIndex: 0, EndIndex: last},
	)

	assertBlocks(t, Blocks(p), []string{"ru:Единственное предложение."})
}

func TestBlocksWithoutEnrichmentFallsBackToTheOriginal(t *testing.T) {
	p := &model.ArticlePayload{
		OriginalText: "Lead paragraph.\n\nSecond paragraph.",
		Tokens:       tokenize.Tokenize("Lead paragraph.\n\nSecond paragraph."),
	}

	assertBlocks(t, Blocks(p), []string{"orig:Lead paragraph.", "orig:Second paragraph."})
}

func TestBlocksOfEmptyPayloadIsEmpty(t *testing.T) {
	if got := Blocks(&model.ArticlePayload{}); len(got) != 0 {
		t.Fatalf("Blocks of an empty payload = %v, want none", got)
	}
	if got := Blocks(nil); len(got) != 0 {
		t.Fatalf("Blocks(nil) = %v, want none", got)
	}
}
