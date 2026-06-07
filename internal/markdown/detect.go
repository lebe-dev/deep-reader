package markdown

import (
	"regexp"
	"strings"

	"deep-reader/internal/model"
)

// boldRe matches inline strong emphasis (**text** or __text__) with non-empty,
// non-space-padded content — the common, unambiguous form.
var boldRe = regexp.MustCompile(`(\*\*|__)(?:[^\s*_].*?)(\*\*|__)`)

// inlineCodeRe matches inline code spans (`code`).
var inlineCodeRe = regexp.MustCompile("`[^`\n]+`")

// italicRe matches single-marker emphasis (*text* or _text_) with non-empty,
// non-space-padded content. It also matches the inner run of a **bold** span,
// which only ever pushes emphasis-rich text over the threshold faster — never an
// issue for the single-marker guard (one bold still yields too few signals).
var italicRe = regexp.MustCompile(`(?:\*|_)[^\s*_][^*\n]*?(?:\*|_)`)

// inlineSignalThreshold is the number of inline Markdown markers (bold, inline
// code, links, images) that, in the absence of any block-level structure, is
// taken as enough to call the text Markdown. A lone marker is too weak — a stray
// `*` or a single bracketed phrase appears in ordinary prose — so we require a
// few before flipping the verdict.
const inlineSignalThreshold = 3

// DetectFormat classifies text as model.ContentFormatMarkdown or
// model.ContentFormatPlain. It is the single entry point used at ingest to stamp
// Article.ContentFormat. See IsMarkdown for the heuristic.
func DetectFormat(text string) string {
	if IsMarkdown(text) {
		return model.ContentFormatMarkdown
	}
	return model.ContentFormatPlain
}

// IsMarkdown reports whether text is structured Markdown rather than plain prose.
//
// It is a deliberately conservative heuristic, not a parser: any single
// block-level construct (heading, fenced code, blockquote, table, thematic
// break, or a list of two or more items) is decisive, since none of these occur
// by accident in prose. Failing that, it falls back to counting inline markers
// (bold, inline code, links, images) and calls the text Markdown only once a few
// have accumulated — so a paragraph with one stray `*` or a single link stays
// plain. The goal is to avoid misclassifying ordinary pasted prose, where the
// failure mode (leaking a `#` or `*`) is more jarring than treating genuine
// Markdown as plain.
func IsMarkdown(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}

	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")

	listItems := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// A fenced code block (``` or ~~~) is itself decisive.
		if fenceMarker(trimmed) != "" {
			return true
		}

		switch {
		case headingRe.MatchString(line):
			return true
		case blockquoteRe.MatchString(line):
			return true
		case isHorizontalRule(trimmed):
			return true
		case isTableDelimiter(trimmed):
			return true
		case listMarkerRe.MatchString(line):
			listItems++
			if listItems >= 2 {
				return true
			}
		}
	}

	inlineSignals := len(boldRe.FindAllString(text, -1)) +
		len(italicRe.FindAllString(text, -1)) +
		len(inlineCodeRe.FindAllString(text, -1)) +
		len(imageRe.FindAllString(text, -1)) +
		len(inlineLinkRe.FindAllString(text, -1))
	return inlineSignals >= inlineSignalThreshold
}
