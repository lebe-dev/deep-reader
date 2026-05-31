package markdown

import (
	"regexp"
	"strings"
)

// markdownToText converts the Markdown returned by markdown.new into plain prose
// suitable for the deterministic tokenizer and the reader UI. The deterministic
// tokenizer ignores punctuation, but raw Markdown still leaks noise the reader
// would see and that pollutes tokens — most importantly link/image URLs (which
// tokenize into junk words like "https", "com"). This strips that structure
// while preserving the readable text.
//
// It is intentionally regex/line based rather than a full CommonMark parser:
// articles are prose, the failure mode of an unhandled construct is a stray
// marker (cosmetic), and we avoid a heavy dependency for a self-hosted binary.
func markdownToText(md string) string {
	md = stripFrontmatter(md)
	md = strings.ReplaceAll(md, "\r\n", "\n")

	lines := strings.Split(md, "\n")
	out := make([]string, 0, len(lines))

	inFence := false
	var fence string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Fenced code blocks (``` or ~~~): drop the fences and their content.
		if marker := fenceMarker(trimmed); marker != "" {
			if inFence {
				if strings.HasPrefix(trimmed, fence) {
					inFence = false
				}
			} else {
				inFence = true
				fence = marker
			}
			continue
		}
		if inFence {
			continue
		}

		// Thematic breaks (---, ***, ___) and table delimiter rows (|---|:--:|).
		if isHorizontalRule(trimmed) || isTableDelimiter(trimmed) {
			continue
		}

		line = stripBlockPrefix(line)
		line = inlineClean(line)
		out = append(out, line)
	}

	return collapseBlankLines(strings.Join(out, "\n"))
}

var (
	frontmatterRe   = regexp.MustCompile(`(?s)\A---\n.*?\n---\n?`)
	headingRe       = regexp.MustCompile(`^\s{0,3}#{1,6}\s+`)
	blockquoteRe    = regexp.MustCompile(`^\s*>\s?`)
	listMarkerRe    = regexp.MustCompile(`^\s*([-*+]|\d+[.)])\s+`)
	imageRe         = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	inlineLinkRe    = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	referenceLinkRe = regexp.MustCompile(`\[([^\]]*)\]\[[^\]]*\]`)
	autolinkRe      = regexp.MustCompile(`<https?://[^>\s]+>`)
	tableDelimRe    = regexp.MustCompile(`^\|?[\s:|-]+\|[\s:|-]*$`)
	blankRunRe      = regexp.MustCompile(`\n{3,}`)
)

// stripFrontmatter removes a leading YAML front-matter block (--- ... ---).
func stripFrontmatter(md string) string {
	return frontmatterRe.ReplaceAllString(md, "")
}

// fenceMarker returns "```" or "~~~" if trimmed opens/closes a code fence, else "".
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

// isHorizontalRule reports whether trimmed is a thematic break: three or more
// of the same marker (-, *, or _), optionally separated by spaces, and nothing
// else. Go's RE2 has no backreferences, so this is checked programmatically.
func isHorizontalRule(trimmed string) bool {
	var marker rune
	count := 0
	for _, r := range trimmed {
		switch r {
		case ' ', '\t':
			continue
		case '-', '*', '_':
			if count == 0 {
				marker = r
			} else if r != marker {
				return false
			}
			count++
		default:
			return false
		}
	}
	return count >= 3
}

// isTableDelimiter reports whether trimmed is a Markdown table delimiter row.
func isTableDelimiter(trimmed string) bool {
	return strings.Contains(trimmed, "|") && tableDelimRe.MatchString(trimmed)
}

// stripBlockPrefix removes leading heading hashes, blockquote markers, and list
// bullets/numbers from a single line.
func stripBlockPrefix(line string) string {
	line = headingRe.ReplaceAllString(line, "")
	// Blockquotes can nest (> > text); peel each level.
	for blockquoteRe.MatchString(line) {
		line = blockquoteRe.ReplaceAllString(line, "")
	}
	line = listMarkerRe.ReplaceAllString(line, "")
	return line
}

// inlineClean removes inline Markdown: images, links (keeping their text),
// autolinks, and emphasis/code markers.
func inlineClean(line string) string {
	line = imageRe.ReplaceAllString(line, "")
	line = inlineLinkRe.ReplaceAllString(line, "$1")
	line = referenceLinkRe.ReplaceAllString(line, "$1")
	line = autolinkRe.ReplaceAllString(line, "")
	// Emphasis / strikethrough / inline code markers. Single "_" is left intact
	// so snake_case identifiers survive; markdown.new rarely emits "_italic_".
	for _, marker := range []string{"**", "__", "~~", "`", "*"} {
		line = strings.ReplaceAll(line, marker, "")
	}
	// Table cell pipes → spaces so adjacent cells don't fuse into one token.
	line = strings.ReplaceAll(line, "|", " ")
	return strings.TrimSpace(line)
}

// collapseBlankLines trims the document and collapses runs of 3+ newlines to a
// single blank-line separator.
func collapseBlankLines(text string) string {
	return strings.TrimSpace(blankRunRe.ReplaceAllString(text, "\n\n"))
}
