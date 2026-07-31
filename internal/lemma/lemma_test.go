package lemma_test

import (
	"strings"
	"sync"
	"testing"

	"deep-reader/internal/lemma"
	"deep-reader/internal/model"
)

func newLemmatizer(t *testing.T) *lemma.Lemmatizer {
	t.Helper()
	l, err := lemma.New()
	if err != nil {
		t.Fatalf("lemma.New: %v", err)
	}
	return l
}

func TestLemmaRegularInflections(t *testing.T) {
	l := newLemmatizer(t)
	cases := map[string]string{
		"cars":       "car",
		"boxes":      "box",
		"cities":     "city",
		"walked":     "walk",
		"walking":    "walk",
		"bigger":     "big",
		"resilient":  "resilient",
		"ubiquitous": "ubiquitous",
	}
	for in, want := range cases {
		if got := l.Lemma(in); got != want {
			t.Errorf("Lemma(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLemmaIrregulars(t *testing.T) {
	l := newLemmatizer(t)
	cases := map[string]string{
		"ran":      "run",
		"went":     "go",
		"children": "child",
		"mice":     "mouse",
		"better":   "good",
		"was":      "be",
	}
	for in, want := range cases {
		if got := l.Lemma(in); got != want {
			t.Errorf("Lemma(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLemmaLowercasesAndPassesUnknownsThrough(t *testing.T) {
	l := newLemmatizer(t)
	// Case is normalized away — the vocabulary is case-insensitive.
	if got := l.Lemma("Running"); got != "run" {
		t.Errorf("Lemma(%q) = %q, want %q", "Running", got, "run")
	}
	// Unknown forms come back as themselves, lowercased, never empty.
	for _, in := range []string{"Kubernetes", "zzzqqx", "3.14", "well-known", ""} {
		want := strings.ToLower(in)
		if got := l.Lemma(in); got != want {
			t.Errorf("Lemma(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnnotateOmitsLemmaWhenEqualToLoweredText(t *testing.T) {
	l := newLemmatizer(t)
	tokens := []model.Token{
		{Index: 0, Text: "The", Start: 0, End: 3},
		{Index: 1, Text: "children", Start: 4, End: 12},
		{Index: 2, Text: "ran", Start: 13, End: 16},
		{Index: 3, Text: "home", Start: 17, End: 21},
	}
	got := l.Annotate(tokens)

	if len(got) != 4 {
		t.Fatalf("Annotate changed token count: got %d, want 4", len(got))
	}
	// "the" and "home" are their own lemmas → omitted.
	if got[0].Lemma != "" || got[3].Lemma != "" {
		t.Errorf("expected omitted lemmas, got %q and %q", got[0].Lemma, got[3].Lemma)
	}
	if got[1].Lemma != "child" {
		t.Errorf("children → %q, want %q", got[1].Lemma, "child")
	}
	if got[2].Lemma != "run" {
		t.Errorf("ran → %q, want %q", got[2].Lemma, "run")
	}
}

func TestAnnotatePreservesIndicesAndOffsets(t *testing.T) {
	l := newLemmatizer(t)
	in := []model.Token{
		{Index: 0, Text: "Mice", Start: 0, End: 4},
		{Index: 1, Text: "were", Start: 5, End: 9},
	}
	want := append([]model.Token(nil), in...)

	got := l.Annotate(in)
	for i := range got {
		if got[i].Index != want[i].Index || got[i].Text != want[i].Text ||
			got[i].Start != want[i].Start || got[i].End != want[i].End {
			t.Errorf("token %d mutated beyond Lemma: got %+v, want base %+v", i, got[i], want[i])
		}
	}
}

func TestAnnotateIsIdempotent(t *testing.T) {
	l := newLemmatizer(t)
	tokens := l.Annotate([]model.Token{{Index: 0, Text: "children", End: 8}})
	again := l.Annotate(append([]model.Token(nil), tokens...))
	if again[0].Lemma != tokens[0].Lemma {
		t.Errorf("re-annotation changed lemma: %q → %q", tokens[0].Lemma, again[0].Lemma)
	}
}

func TestLemmaIsConcurrencySafe(t *testing.T) {
	l := newLemmatizer(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 500; n++ {
				if got := l.Lemma("children"); got != "child" {
					t.Errorf("concurrent Lemma = %q, want child", got)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func BenchmarkLemma(b *testing.B) {
	l, err := lemma.New()
	if err != nil {
		b.Fatalf("lemma.New: %v", err)
	}
	words := []string{"children", "ran", "resilient", "the", "ubiquitous", "walking", "Kubernetes"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Lemma(words[i%len(words)])
	}
}
