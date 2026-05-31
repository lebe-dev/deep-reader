// Package tokenize implements the deterministic tokenizer for Deep Reader.
//
// # Contract
//
// Tokenize(text) returns a slice of model.Token where every token is a "word"
// token: a contiguous run of letter/digit runes (plus embedded apostrophes for
// contractions, hyphens for compound words, and decimal points for numbers).
// Pure-punctuation characters are never emitted as tokens, but they do act as
// split boundaries so that downstream code can compute sentence/phrase ranges
// from sequential token indices.
//
// # Design choices
//
//   - Only WORD tokens are emitted.  Pure punctuation (commas, periods,
//     parentheses, etc.) acts as a split point but is not represented as a
//     token in the output.  This simplifies the enrichment layer and the reader
//     UI, which only need word positions.  The byte offsets are still exact, so
//     any caller that needs to locate where punctuation falls in the source text
//     can compute the gaps between consecutive Token.Start/End values.
//
//   - Byte offsets, not rune offsets.  Token.Start and Token.End are byte
//     indices into the original UTF-8 string, matching the Go slice semantics
//     text[t.Start:t.End] == t.Text.  This is the cheapest representation and
//     avoids any mismatch between Go's byte-indexed strings and rune counts.
//
//   - Apostrophe joining (contractions).  An ASCII apostrophe (U+0027) or the
//     typographic right single quotation mark (U+2019 ' ) between two letter
//     runes joins them into one token: "don't", "it's", "o'clock".  An
//     apostrophe at the start or end of a word token is stripped (it is
//     treated as a quotation mark, not a contraction marker).
//
//   - Hyphen joining (compounds).  An ASCII hyphen between two letter runes
//     joins them: "well-known", "state-of-the-art".  A hyphen at the edge of a
//     word (leading or trailing) is punctuation and causes a split.
//
//   - Decimal numbers.  A period between two digit runes joins them into one
//     token ("3.14").  Other uses of a period (sentence-ending, ellipsis) cause
//     a split.
//
//   - Pure, deterministic, no I/O.  The function has no side effects, takes no
//     configuration, and always produces the same output for the same input.
package tokenize

import (
	"unicode"
	"unicode/utf8"

	"deep-reader/internal/model"
)

// apostropheRune is the typographic right single quotation mark (U+2019).
const apostropheRune = '’'

// isApostrophe returns true for an ASCII apostrophe or the typographic right
// single quotation mark — both are used as contraction markers in English text.
func isApostrophe(r rune) bool {
	return r == '\'' || r == apostropheRune
}

// isWordRune returns true for runes that form the core of a word token:
// Unicode letters and decimal digits.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// Tokenize splits text into word tokens, preserving exact byte offsets.
//
// The tokenizer is a single-pass scanner.  It accumulates runes into a
// candidate token while tracking whether each inter-word character (apostrophe,
// hyphen, period) qualifies as a "join" or a "split".  Joining is confirmed
// lazily: the join character is only included when a subsequent word rune
// follows immediately.
//
// The function signature matches the ports.go contract:
//
//	tokenize.Tokenize(text string) []model.Token
func Tokenize(text string) []model.Token {
	if len(text) == 0 {
		return nil
	}

	tokens := make([]model.Token, 0, estimateTokenCount(text))

	// Scanning state.
	type pendingJoin struct {
		byteStart int  // byte index of the join character in text
		byteLen   int  // byte length of the join character
		r         rune // the join rune itself
	}

	inToken := false
	tokStart := 0            // byte start of current token
	tokEnd := 0              // byte end of current token (exclusive), updated as we go
	var pending *pendingJoin // a join candidate between word-rune runs
	prevWasDigit := false    // whether the last emitted word rune was a digit

	// flushPending discards the buffered join character, closing the current
	// token at tokEnd and leaving the scanner ready to start a new token.
	flushToken := func() {
		if inToken {
			tokText := text[tokStart:tokEnd]
			tokens = append(tokens, model.Token{
				Index: len(tokens),
				Text:  tokText,
				Start: tokStart,
				End:   tokEnd,
			})
		}
		inToken = false
		pending = nil
		prevWasDigit = false
	}

	i := 0
	for i < len(text) {
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size == 1 {
			// Invalid UTF-8 byte: treat as whitespace (split).
			flushToken()
			i++
			continue
		}

		bytePos := i
		i += size

		switch {
		case isWordRune(r):
			if !inToken {
				// Starting a new token.
				inToken = true
				tokStart = bytePos
			} else if pending != nil {
				// A word rune follows a pending join character.
				// Decide whether the join is valid.
				joinOK := false
				switch {
				case isApostrophe(pending.r):
					// Apostrophe joins letter to letter only.
					joinOK = unicode.IsLetter(r)
				case pending.r == '-':
					// Hyphen joins letter/digit to letter only.
					joinOK = unicode.IsLetter(r)
				case pending.r == '.':
					// Decimal point joins digit to digit only.
					joinOK = unicode.IsDigit(r) && prevWasDigit
				}
				if !joinOK {
					// Emit current token up to tokEnd, then start fresh.
					flushToken()
					inToken = true
					tokStart = bytePos
				}
				// Whether joined or not, pending is now consumed.
				pending = nil
			}
			tokEnd = bytePos + size
			prevWasDigit = unicode.IsDigit(r)

		case isApostrophe(r) || r == '-' || r == '.':
			if !inToken {
				// Leading join char — ignore (punctuation boundary).
				break
			}
			// Park as a pending join; will be confirmed if a word rune follows.
			pending = &pendingJoin{byteStart: bytePos, byteLen: size, r: r}

		default:
			// Whitespace or other punctuation — hard split.
			flushToken()
		}
	}

	// Flush any open token at end of input.
	flushToken()

	return tokens
}

// estimateTokenCount returns a rough pre-allocation size for the token slice.
// Using 1 token per ~6 bytes is a reasonable average for English text.
func estimateTokenCount(text string) int {
	n := len(text) / 6
	if n < 8 {
		n = 8
	}
	return n
}
