// Package publish turns an enriched article into a self-contained public page.
//
// The page is a single static HTML file: Open Graph metadata in the head (so
// link previews work without any server-side rendering per request) and the
// article's sentence translations as the body. It is generated once, when the
// user publishes, and written to disk by Publisher; serving it is a file read
// behind a TTL check, never a template render.
//
// Only sentence translations make it into the page — the public reader is a
// plain read, with no tapping, no word-level overlay, and no offline cache.
// Stretches of the article that enrichment never covered (coverage below 100%)
// are carried over in the original language rather than dropped, so the text
// never silently loses a paragraph.
package publish

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"deep-reader/internal/model"
)

// Segment is one run of page text: either a sentence translation or a stretch
// of the source article that enrichment left uncovered.
type Segment struct {
	Text string
	// Translated distinguishes a translation from an original-language
	// fallback, which the page renders in a muted style and marks with the
	// source language for screen readers.
	Translated bool
}

// Block is a paragraph of the rendered page.
type Block struct {
	Segments []Segment
}

// paragraphSep matches a blank line in the source text — the only paragraph
// signal available, since the token stream carries no structure of its own.
var paragraphSep = regexp.MustCompile(`\n[ \t\r]*\n`)

// Blocks renders the payload into paragraphs of translated text.
//
// Sentence translations are laid out in source order, and the source text
// between two consecutive translated spans is emitted untranslated. Paragraph
// breaks come from blank lines in the original text, so the published page
// keeps the article's shape.
func Blocks(p *model.ArticlePayload) []Block {
	if p == nil {
		return nil
	}

	b := &builder{}
	cursor := 0

	for _, s := range orderedSentences(p) {
		start, end := p.Tokens[s.StartIndex].Start, absorbTrailingPunct(p.OriginalText, p.Tokens[s.EndIndex].End)
		if end <= cursor {
			continue
		}
		if start > cursor {
			b.gap(p.OriginalText[cursor:start])
		}
		b.add(s.Translation, true)
		cursor = end
	}
	if cursor < len(p.OriginalText) {
		b.gap(p.OriginalText[cursor:])
	}

	return b.done()
}

// closingPunct is the punctuation that belongs to the sentence it follows. The
// tokenizer keeps only words, so a sentence span ends at the last word and the
// full stop sits just outside it; without absorbing it here every sentence
// would be followed on the page by a stray "." carried over as untranslated
// source text.
const closingPunct = `.?!,;:…)]}"'»”’`

// absorbTrailingPunct extends a span's end offset over the punctuation that
// immediately follows it.
func absorbTrailingPunct(text string, end int) int {
	for end < len(text) {
		r, size := utf8.DecodeRuneInString(text[end:])
		if !strings.ContainsRune(closingPunct, r) {
			break
		}
		end += size
	}
	return end
}

// orderedSentences returns the payload's sentence translations in source order,
// dropping the ones a page cannot use: out-of-range or inverted token spans,
// empty translations, and spans that overlap an earlier one (which would
// duplicate text on the page).
func orderedSentences(p *model.ArticlePayload) []model.Sentence {
	if p == nil || p.Enrichment == nil {
		return nil
	}

	out := make([]model.Sentence, 0, len(p.Enrichment.Sentences))
	for _, s := range p.Enrichment.Sentences {
		if s.StartIndex < 0 || s.EndIndex < s.StartIndex || s.EndIndex >= len(p.Tokens) {
			continue
		}
		if strings.TrimSpace(s.Translation) == "" {
			continue
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartIndex < out[j].StartIndex })

	deduped := out[:0]
	prevEnd := -1
	for _, s := range out {
		if s.StartIndex <= prevEnd {
			continue
		}
		deduped = append(deduped, s)
		prevEnd = s.EndIndex
	}
	return deduped
}

// builder accumulates segments into paragraphs.
type builder struct {
	out []Block
	cur []Segment
}

func (b *builder) add(text string, translated bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	b.cur = append(b.cur, Segment{Text: text, Translated: translated})
}

// br closes the current paragraph. Closing an empty one is a no-op, so runs of
// blank lines do not produce empty blocks.
func (b *builder) br() {
	if len(b.cur) == 0 {
		return
	}
	b.out = append(b.out, Block{Segments: b.cur})
	b.cur = nil
}

// gap emits a stretch of untranslated source text, honouring the paragraph
// breaks inside it.
func (b *builder) gap(raw string) {
	for i, part := range paragraphSep.Split(raw, -1) {
		if i > 0 {
			b.br()
		}
		b.add(part, false)
	}
}

func (b *builder) done() []Block {
	b.br()
	return b.out
}
