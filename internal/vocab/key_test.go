package vocab_test

import (
	"encoding/json"
	"os"
	"testing"

	"deep-reader/internal/model"
	"deep-reader/internal/vocab"
)

// goldenTable mirrors testdata/normalize_golden.json, which the TypeScript key
// builder reads too (frontend/src/lib/vocab/key.test.ts). It is the pin that
// keeps the two implementations from drifting.
type goldenTable struct {
	Normalize []struct {
		In  string `json:"in"`
		Out string `json:"out"`
	} `json:"normalize"`
	Keys []struct {
		Kind string `json:"kind"`
		Lang string `json:"lang"`
		Base string `json:"base"`
		Out  string `json:"out"`
	} `json:"keys"`
}

func loadGolden(t *testing.T) goldenTable {
	t.Helper()
	raw, err := os.ReadFile("testdata/normalize_golden.json")
	if err != nil {
		t.Fatalf("read golden table: %v", err)
	}
	var table goldenTable
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatalf("decode golden table: %v", err)
	}
	if len(table.Normalize) == 0 || len(table.Keys) == 0 {
		t.Fatal("golden table is empty")
	}
	return table
}

func TestNormalizeMatchesGoldenTable(t *testing.T) {
	for _, c := range loadGolden(t).Normalize {
		if got := vocab.Normalize(c.In); got != c.Out {
			t.Errorf("Normalize(%q) = %q, want %q", c.In, got, c.Out)
		}
	}
}

func TestKeyMatchesGoldenTable(t *testing.T) {
	for _, c := range loadGolden(t).Keys {
		if got := vocab.Key(c.Kind, c.Lang, c.Base); got != c.Out {
			t.Errorf("Key(%q, %q, %q) = %q, want %q", c.Kind, c.Lang, c.Base, got, c.Out)
		}
	}
}

func TestLemmaOfFallsBackToLoweredText(t *testing.T) {
	// The omitempty encoding means an absent Lemma is the common case, not an error.
	if got := vocab.LemmaOf(model.Token{Text: "Home"}); got != "home" {
		t.Errorf("LemmaOf(no lemma) = %q, want %q", got, "home")
	}
	if got := vocab.LemmaOf(model.Token{Text: "children", Lemma: "child"}); got != "child" {
		t.Errorf("LemmaOf(with lemma) = %q, want %q", got, "child")
	}
}

func TestWordKeyUsesLemma(t *testing.T) {
	got := vocab.WordKey("ru", model.Token{Text: "Children", Lemma: "child"})
	if want := "word:ru:child"; got != want {
		t.Errorf("WordKey = %q, want %q", got, want)
	}
}

func TestPhraseKeyJoinsLemmasOfRange(t *testing.T) {
	tokens := []model.Token{
		{Index: 0, Text: "He"},
		{Index: 1, Text: "spilled", Lemma: "spill"},
		{Index: 2, Text: "the"},
		{Index: 3, Text: "beans", Lemma: "bean"},
		{Index: 4, Text: "again"},
	}
	got := vocab.PhraseKey("ru", tokens, 1, 3)
	if want := "phrase:ru:spill the bean"; got != want {
		t.Errorf("PhraseKey = %q, want %q", got, want)
	}

	// An inflected variant of the same phrase must land on the same entry.
	inflected := []model.Token{
		{Index: 0, Text: "spilling", Lemma: "spill"},
		{Index: 1, Text: "the"},
		{Index: 2, Text: "beans", Lemma: "bean"},
	}
	if other := vocab.PhraseKey("ru", inflected, 0, 2); other != got {
		t.Errorf("inflected phrase key = %q, want %q", other, got)
	}
}

func TestPhraseKeyRejectsBadRanges(t *testing.T) {
	tokens := []model.Token{{Index: 0, Text: "one"}, {Index: 1, Text: "two"}}
	for _, c := range []struct{ start, end int }{{-1, 1}, {1, 0}, {0, 5}, {5, 6}} {
		if got := vocab.PhraseKey("ru", tokens, c.start, c.end); got != "" {
			t.Errorf("PhraseKey(%d,%d) = %q, want empty", c.start, c.end, got)
		}
	}
}
