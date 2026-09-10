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
	"sort"
	"strings"
	"unicode/utf8"

	"deep-reader/internal/markdown"
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

// Node is one element of the rendered page: a block of text, or a quote level
// holding the nodes nested inside it.
//
// A comment thread is a tree — each reply one quote level deeper than the
// comment it answers — so the page is one too. An ordinary article yields a
// flat list of paragraph nodes, exactly what published pages have always been.
type Node struct {
	// Kind is one of the unit kinds (paragraph, heading, code, rule) or "quote"
	// for a nesting level. The page template switches on it.
	Kind string
	// Level is the heading level for a heading node, 0 otherwise.
	Level int
	// Segments is the node's text, in source order.
	Segments []Segment
	// Comment marks a quote level that opens with a heading — in a thread that
	// heading is the author of the comment, so the level renders as an indented
	// column of ordinary text rather than as a quotation.
	Comment bool
	// Children are the nodes of a quote level.
	Children []Node
}

// Quote is the Kind of a nesting level; the other kinds come from the source
// structure (see structure.go).
const nodeQuote = "quote"

// Nodes renders the payload into the tree the public page prints.
//
// Sentence translations are laid out in source order and the source text
// between two consecutive translated spans is emitted untranslated, so a page
// never silently loses a paragraph. Each piece is placed in the block of the
// source it came from, which is what keeps a thread's structure — and the
// Markdown markers that encode it stay out of the page, since only the visible
// ranges of each line are ever read.
func Nodes(p *model.ArticlePayload) []Node {
	if p == nil {
		return nil
	}

	isMarkdown := p.ContentFormat == model.ContentFormatMarkdown
	units := parseUnits(p.OriginalText, isMarkdown)
	if len(units) == 0 {
		return nil
	}
	segments := make([][]Segment, len(units))

	cursor := 0
	for _, s := range orderedSentences(p) {
		start, end := p.Tokens[s.StartIndex].Start, absorbTrailingPunct(p.OriginalText, p.Tokens[s.EndIndex].End)
		if end <= cursor {
			continue
		}
		if start > cursor {
			emitGap(segments, units, p.OriginalText, cursor, start, isMarkdown)
		}
		if idx := unitAt(units, start); idx >= 0 {
			if text := strings.TrimSpace(s.Translation); text != "" {
				segments[idx] = append(segments[idx], Segment{Text: text, Translated: true})
			}
		}
		cursor = end
	}
	if cursor < len(p.OriginalText) {
		emitGap(segments, units, p.OriginalText, cursor, len(p.OriginalText), isMarkdown)
	}

	return nest(units, segments)
}

// emitGap distributes a stretch of untranslated source over the units it spans,
// reading only the visible range of each line so quote markers and heading
// hashes never reach the page.
func emitGap(segments [][]Segment, units []unit, text string, from, to int, isMarkdown bool) {
	for i, u := range units {
		if u.end <= from || u.start >= to {
			continue
		}
		parts := make([]string, 0, len(u.lines))
		for _, line := range u.lines {
			start, end := max(line.start, from), min(line.end, to)
			if start >= end {
				continue
			}
			parts = append(parts, text[start:end])
		}
		if len(parts) == 0 {
			continue
		}
		sep := " "
		if u.kind == unitCode {
			// Code is the one place where the line breaks are the content.
			sep = "\n"
		}
		joined := strings.TrimSpace(strings.Join(parts, sep))
		// Inline markers are structure too: printed as text they are the same
		// defect as a visible quote marker. Code is the exception — there the
		// asterisks and backticks are the content.
		if isMarkdown && u.kind != unitCode {
			joined = markdown.CleanInline(joined)
		}
		if joined == "" {
			continue
		}
		segments[i] = append(segments[i], Segment{Text: joined})
	}
}

// unitAt returns the index of the unit containing the source offset, or the
// first unit that starts after it. It returns -1 only for an empty list.
func unitAt(units []unit, offset int) int {
	for i, u := range units {
		if offset < u.end {
			return i
		}
	}
	if len(units) == 0 {
		return -1
	}
	return len(units) - 1
}

// nest turns the flat unit list into the quote tree, dropping units that ended
// up with nothing to show (a heading whose text was swallowed by a translation
// span, say).
func nest(units []unit, segments [][]Segment) []Node {
	var root []Node
	// stack[i] is the quote level i+1 currently open; the parent chain is walked
	// on the way back to append a finished level.
	var stack []*Node

	appendNode := func(n Node) {
		if len(stack) == 0 {
			root = append(root, n)
			return
		}
		top := stack[len(stack)-1]
		top.Children = append(top.Children, n)
	}

	for i, u := range units {
		if u.kind != unitRule && len(segments[i]) == 0 {
			continue
		}

		keep := min(u.depth, u.breakDepth)
		for len(stack) > keep {
			closed := *stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			closed.Comment = hasHeading(closed.Children)
			appendNode(closed)
		}
		for len(stack) < u.depth {
			stack = append(stack, &Node{Kind: nodeQuote})
		}

		appendNode(Node{Kind: u.kind, Level: u.level, Segments: segments[i]})
	}

	for len(stack) > 0 {
		closed := *stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		closed.Comment = hasHeading(closed.Children)
		appendNode(closed)
	}

	return root
}

// hasHeading reports whether a quote level opens a comment rather than a
// quotation: a comment carries the author line the thread renderer wrote.
func hasHeading(nodes []Node) bool {
	for _, n := range nodes {
		if n.Kind == unitHeading {
			return true
		}
	}
	return false
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
