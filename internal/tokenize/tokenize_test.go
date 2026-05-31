package tokenize_test

import (
	"testing"
	"unicode/utf8"

	"deep-reader/internal/model"
	"deep-reader/internal/tokenize"
)

// helper: verify that every token's offsets round-trip back to the token text.
func assertOffsets(t *testing.T, text string, tokens []model.Token) {
	t.Helper()
	for _, tok := range tokens {
		got := text[tok.Start:tok.End]
		if got != tok.Text {
			t.Errorf("offset mismatch: text[%d:%d]=%q but token.Text=%q",
				tok.Start, tok.End, got, tok.Text)
		}
		if tok.End > len(text) {
			t.Errorf("token %q End=%d beyond text length %d", tok.Text, tok.End, len(text))
		}
	}
}

// helper: extract just the text strings from a token slice.
func texts(tokens []model.Token) []string {
	out := make([]string, len(tokens))
	for i, t := range tokens {
		out[i] = t.Text
	}
	return out
}

// helper: assert sequential zero-based indices.
func assertIndices(t *testing.T, tokens []model.Token) {
	t.Helper()
	for i, tok := range tokens {
		if tok.Index != i {
			t.Errorf("token %d has Index=%d, want %d", i, tok.Index, i)
		}
	}
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Basic word splitting
// ---------------------------------------------------------------------------

func TestSimpleWords(t *testing.T) {
	text := "Hello world"
	toks := tokenize.Tokenize(text)
	want := []string{"Hello", "world"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
	assertIndices(t, toks)
}

func TestPunctuationExcluded(t *testing.T) {
	// Pure punctuation tokens must not be emitted.
	text := "Hello, world! How are you?"
	toks := tokenize.Tokenize(text)
	want := []string{"Hello", "world", "How", "are", "you"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

func TestMultipleSpaces(t *testing.T) {
	text := "one   two\t\tthree"
	toks := tokenize.Tokenize(text)
	want := []string{"one", "two", "three"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

// ---------------------------------------------------------------------------
// Contractions — ASCII apostrophe
// ---------------------------------------------------------------------------

func TestContractionDont(t *testing.T) {
	text := "I don't know"
	toks := tokenize.Tokenize(text)
	want := []string{"I", "don't", "know"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

func TestContractionIts(t *testing.T) {
	text := "it's fine"
	toks := tokenize.Tokenize(text)
	want := []string{"it's", "fine"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

func TestContractionWere(t *testing.T) {
	text := "we're ready"
	toks := tokenize.Tokenize(text)
	want := []string{"we're", "ready"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

func TestContractionCant(t *testing.T) {
	text := "I can't stop"
	toks := tokenize.Tokenize(text)
	want := []string{"I", "can't", "stop"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

func TestContractionWont(t *testing.T) {
	text := "won't work"
	toks := tokenize.Tokenize(text)
	want := []string{"won't", "work"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

func TestContractionHell(t *testing.T) {
	text := "he'll come"
	toks := tokenize.Tokenize(text)
	want := []string{"he'll", "come"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

func TestContractionTheyve(t *testing.T) {
	text := "they've gone"
	toks := tokenize.Tokenize(text)
	want := []string{"they've", "gone"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

func TestContractionIm(t *testing.T) {
	text := "I'm here"
	toks := tokenize.Tokenize(text)
	want := []string{"I'm", "here"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

func TestContractionOclock(t *testing.T) {
	text := "at 5 o'clock sharp"
	toks := tokenize.Tokenize(text)
	want := []string{"at", "5", "o'clock", "sharp"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

// ---------------------------------------------------------------------------
// Contractions — typographic (right single quotation mark U+2019)
// ---------------------------------------------------------------------------

func TestContractionTypographicApostrophe(t *testing.T) {
	// U+2019 RIGHT SINGLE QUOTATION MARK — common in "smart quotes" typography.
	text := "don’t worry"
	toks := tokenize.Tokenize(text)
	want := []string{"don’t", "worry"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

func TestContractionTypographicIts(t *testing.T) {
	text := "it’s okay"
	toks := tokenize.Tokenize(text)
	want := []string{"it’s", "okay"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

// ---------------------------------------------------------------------------
// Hyphenated words
// ---------------------------------------------------------------------------

func TestHyphenatedWord(t *testing.T) {
	text := "well-known fact"
	toks := tokenize.Tokenize(text)
	want := []string{"well-known", "fact"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

func TestHyphenatedMultipleParts(t *testing.T) {
	text := "state-of-the-art design"
	toks := tokenize.Tokenize(text)
	want := []string{"state-of-the-art", "design"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

// A trailing hyphen (e.g. at a line break) should NOT join with the next word.
func TestTrailingHyphenNotJoined(t *testing.T) {
	text := "some- thing"
	toks := tokenize.Tokenize(text)
	// "some-" has trailing hyphen not followed by a letter — should split.
	// Exact tokenisation: "some" (the hyphen is punctuation, stripped) and "thing".
	// We only require that the two word parts are separate tokens.
	if len(toks) != 2 {
		t.Errorf("got %d tokens %v, want 2 (some, thing)", len(toks), texts(toks))
	}
	assertOffsets(t, text, toks)
}

// A leading hyphen should not join with the next word.
func TestLeadingHyphenNotJoined(t *testing.T) {
	text := "foo -bar"
	toks := tokenize.Tokenize(text)
	if len(toks) != 2 {
		t.Errorf("got %d tokens %v, want 2", len(toks), texts(toks))
	}
	assertOffsets(t, text, toks)
}

// ---------------------------------------------------------------------------
// Numbers
// ---------------------------------------------------------------------------

func TestIntegerNumber(t *testing.T) {
	text := "page 42 of"
	toks := tokenize.Tokenize(text)
	want := []string{"page", "42", "of"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

func TestDecimalNumber(t *testing.T) {
	text := "pi is 3.14 approximately"
	toks := tokenize.Tokenize(text)
	want := []string{"pi", "is", "3.14", "approximately"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

func TestNumberWithComma(t *testing.T) {
	// A comma between digits (e.g. "1,000") is treated as punctuation
	// separating two number tokens, not as part of the number.
	text := "earned 1,000 dollars"
	toks := tokenize.Tokenize(text)
	// The comma is punctuation; "1" and "000" are two separate tokens.
	wantLen := 4 // "earned", "1", "000", "dollars"
	if len(toks) != wantLen {
		t.Errorf("got %d tokens %v, want %d", len(toks), texts(toks), wantLen)
	}
	assertOffsets(t, text, toks)
}

// ---------------------------------------------------------------------------
// Mixed / edge cases
// ---------------------------------------------------------------------------

func TestEmptyString(t *testing.T) {
	toks := tokenize.Tokenize("")
	if len(toks) != 0 {
		t.Errorf("expected no tokens, got %v", toks)
	}
}

func TestOnlyPunctuation(t *testing.T) {
	toks := tokenize.Tokenize("... --- !!!")
	if len(toks) != 0 {
		t.Errorf("expected no tokens, got %v", toks)
	}
}

func TestSentenceWithPeriod(t *testing.T) {
	text := "Hello. World."
	toks := tokenize.Tokenize(text)
	want := []string{"Hello", "World"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

func TestApostropheAtEdge(t *testing.T) {
	// A leading apostrophe is NOT a contraction marker — it's punctuation.
	text := "'hello'"
	toks := tokenize.Tokenize(text)
	want := []string{"hello"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

// ---------------------------------------------------------------------------
// UTF-8 correctness
// ---------------------------------------------------------------------------

func TestUTF8Correctness(t *testing.T) {
	// Text with non-ASCII characters (not English words, but the tokenizer
	// must at least handle them without panicking and produce correct byte
	// offsets for any word-like runes).
	text := "café résumé naïve"
	toks := tokenize.Tokenize(text)
	// Each sequence of letter-class runes is a token.
	if len(toks) == 0 {
		t.Error("expected tokens from UTF-8 text")
	}
	assertOffsets(t, text, toks)
	assertIndices(t, toks)
	// Verify offsets are byte-correct (not rune-correct).
	for _, tok := range toks {
		if !utf8.ValidString(tok.Text) {
			t.Errorf("token %q is not valid UTF-8", tok.Text)
		}
	}
}

func TestUTF8MixedASCII(t *testing.T) {
	text := "hello café world"
	toks := tokenize.Tokenize(text)
	if len(toks) != 3 {
		t.Errorf("got %d tokens %v, want 3", len(toks), texts(toks))
	}
	assertOffsets(t, text, toks)
}

// ---------------------------------------------------------------------------
// Offset invariant: exhaustive round-trip
// ---------------------------------------------------------------------------

func TestOffsetRoundTripComplex(t *testing.T) {
	text := "It's a well-known fact that 3.14 ≈ π, and we're all o'clock fans."
	toks := tokenize.Tokenize(text)
	assertOffsets(t, text, toks)
	assertIndices(t, toks)
	if len(toks) == 0 {
		t.Error("expected tokens")
	}
}

// ---------------------------------------------------------------------------
// Sentence-boundary awareness (pure punctuation exclusion detail)
// ---------------------------------------------------------------------------

func TestSemicolonAndColon(t *testing.T) {
	text := "one: two; three"
	toks := tokenize.Tokenize(text)
	want := []string{"one", "two", "three"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

func TestParentheses(t *testing.T) {
	text := "word (annotation) end"
	toks := tokenize.Tokenize(text)
	want := []string{"word", "annotation", "end"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}

// ---------------------------------------------------------------------------
// Large sequential index check
// ---------------------------------------------------------------------------

func TestSequentialIndices(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog."
	toks := tokenize.Tokenize(text)
	assertIndices(t, toks)
	if len(toks) != 9 {
		t.Errorf("got %d tokens, want 9", len(toks))
	}
	assertOffsets(t, text, toks)
}

// ---------------------------------------------------------------------------
// Newlines and tabs
// ---------------------------------------------------------------------------

func TestNewlines(t *testing.T) {
	text := "first\nsecond\nthird"
	toks := tokenize.Tokenize(text)
	want := []string{"first", "second", "third"}
	if !eq(texts(toks), want) {
		t.Errorf("got %v, want %v", texts(toks), want)
	}
	assertOffsets(t, text, toks)
}
