package publish

import (
	"regexp"
	"strings"
)

// The published page has to reproduce the shape of the source text, and for a
// comment thread that shape carries most of the meaning: a reply is a
// blockquote level deeper than the comment it answers, and each comment opens
// with a heading naming its author (see internal/comments). Rendering such a
// thread as a run of paragraphs — which is what happens when the Markdown
// markers are treated as prose — loses who answered whom and leaves the markers
// themselves visible in the text.
//
// So the source is first parsed into units: the paragraphs, headings, code
// blocks and rules it is made of, each carrying its quote depth and the exact
// source ranges its visible text occupies. Translations and untranslated
// stretches are then distributed into those units by source offset, which keeps
// the existing span logic intact — the structure pass never moves an offset.
//
// This is a deliberately small line-based parser, the same shape as
// internal/markdown/text.go and the reader's markdown-blocks.ts: lists and
// tables are left as paragraphs (their markers survive as text), because a page
// with a stray "-" is a cosmetic problem while a wrong offset is a corrupt one.

// Unit kinds. They map one to one onto the node kinds the page template
// renders.
const (
	unitParagraph = "paragraph"
	unitHeading   = "heading"
	unitCode      = "code"
	unitRule      = "rule"
)

var (
	// quoteMarkerRe matches one blockquote marker at the start of a line.
	quoteMarkerRe = regexp.MustCompile(`^\s{0,3}>\s?`)
	// headingRe matches an ATX heading: the hashes, the space, the text.
	headingRe = regexp.MustCompile(`^(\s{0,3})(#{1,6})(\s+)`)
)

// isRule reports a thematic break: three or more of the same marker (-, * or
// _), spaces allowed, nothing else. RE2 has no backreference, so the "same
// marker" rule is a scan rather than a pattern.
func isRule(trimmed string) bool {
	marker := byte(0)
	count := 0
	for i := 0; i < len(trimmed); i++ {
		c := trimmed[i]
		if c == ' ' || c == '\t' {
			continue
		}
		if c != '-' && c != '*' && c != '_' {
			return false
		}
		if count == 0 {
			marker = c
		} else if c != marker {
			return false
		}
		count++
	}
	return count >= 3
}

// sourceLine is the visible span of one source line: everything after the quote
// markers and the block marker (the heading hashes, say).
type sourceLine struct {
	start int
	end   int
}

// unit is one parsed block of the source: its kind, its quote depth, the source
// span it covers, and the visible ranges inside it.
type unit struct {
	kind  string
	level int // heading level, 0 for every other kind
	depth int // blockquote nesting: 0 at the top level, +1 per reply
	// breakDepth is the shallowest blank line seen since the previous unit. A
	// blank line closes every quote level deeper than itself, which is what
	// separates two quotes at the same depth from one quote that continues.
	breakDepth int
	start      int
	end        int
	lines      []sourceLine
}

// noBreak marks "no blank line since the previous unit"; it is deeper than any
// real depth, so it never closes a level.
const noBreak = int(^uint(0) >> 1)

// parseUnits splits text into its blocks. When isMarkdown is false the text is
// prose and only blank lines separate paragraphs — the behaviour published
// pages had before Markdown articles existed.
func parseUnits(text string, isMarkdown bool) []unit {
	if text == "" {
		return nil
	}

	lines := splitSourceLines(text)
	units := make([]unit, 0, 8)
	breakDepth := noBreak

	appendUnit := func(u unit) {
		u.breakDepth = breakDepth
		breakDepth = noBreak
		units = append(units, u)
	}

	for i := 0; i < len(lines); {
		line := lines[i]
		depth := 0
		content := line
		if isMarkdown {
			content, depth = stripQuoteMarkers(text, line)
		}
		body := strings.TrimSpace(text[content.start:content.end])

		if body == "" {
			breakDepth = min(breakDepth, depth)
			i++
			continue
		}

		if !isMarkdown {
			// Prose: a paragraph runs until the next blank line.
			u := unit{kind: unitParagraph, start: line.start, end: line.end}
			for i < len(lines) && strings.TrimSpace(text[lines[i].start:lines[i].end]) != "" {
				u.lines = append(u.lines, lines[i])
				u.end = lines[i].end
				i++
			}
			appendUnit(u)
			continue
		}

		if fence := fenceMarker(body); fence != "" {
			u := unit{kind: unitCode, depth: depth, start: line.start, end: line.end}
			i++
			for i < len(lines) {
				inner, _ := stripQuoteMarkers(text, lines[i])
				if strings.HasPrefix(strings.TrimSpace(text[inner.start:inner.end]), fence) {
					u.end = lines[i].end
					i++
					break
				}
				u.lines = append(u.lines, inner)
				u.end = lines[i].end
				i++
			}
			appendUnit(u)
			continue
		}

		if m := headingRe.FindStringSubmatch(body); m != nil {
			// The heading marker is measured on the trimmed body, so shift the
			// content start by what the trim itself dropped.
			lead := len(text[content.start:content.end]) - len(strings.TrimLeft(text[content.start:content.end], " \t"))
			start := content.start + lead + len(m[1]) + len(m[2]) + len(m[3])
			appendUnit(unit{
				kind:  unitHeading,
				level: len(m[2]),
				depth: depth,
				start: line.start,
				end:   line.end,
				lines: []sourceLine{{start: start, end: content.end}},
			})
			i++
			continue
		}

		if isRule(body) {
			appendUnit(unit{kind: unitRule, depth: depth, start: line.start, end: line.end})
			i++
			continue
		}

		// Paragraph: consecutive lines at the same depth that start no other
		// block.
		u := unit{kind: unitParagraph, depth: depth, start: line.start, end: line.end}
		for i < len(lines) {
			cur, curDepth := stripQuoteMarkers(text, lines[i])
			curBody := strings.TrimSpace(text[cur.start:cur.end])
			if curBody == "" || curDepth != depth {
				break
			}
			if len(u.lines) > 0 && (headingRe.MatchString(curBody) || fenceMarker(curBody) != "" || isRule(curBody)) {
				break
			}
			u.lines = append(u.lines, cur)
			u.end = lines[i].end
			i++
		}
		appendUnit(u)
	}

	return units
}

// splitSourceLines returns the span of every line of text, excluding the
// newline itself.
func splitSourceLines(text string) []sourceLine {
	lines := make([]sourceLine, 0, strings.Count(text, "\n")+1)
	offset := 0
	for raw := range strings.SplitSeq(text, "\n") {
		lines = append(lines, sourceLine{start: offset, end: offset + len(raw)})
		offset += len(raw) + 1
	}
	return lines
}

// stripQuoteMarkers removes the leading blockquote markers of a line, returning
// what is left and how many markers there were.
func stripQuoteMarkers(text string, line sourceLine) (sourceLine, int) {
	depth := 0
	start := line.start
	for start < line.end {
		m := quoteMarkerRe.FindString(text[start:line.end])
		if m == "" {
			break
		}
		depth++
		start += len(m)
	}
	return sourceLine{start: start, end: line.end}, depth
}

// fenceMarker returns "```" or "~~~" when the line opens or closes a fenced
// code block, else "".
func fenceMarker(trimmed string) string {
	switch {
	case strings.HasPrefix(trimmed, "```"):
		return "```"
	case strings.HasPrefix(trimmed, "~~~"):
		return "~~~"
	default:
		return ""
	}
}
