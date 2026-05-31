package markdown

import (
	"regexp"
	"strings"
	"unicode"
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
	md = unescapeMarkdown(md)

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

// mdPunct is the set of ASCII punctuation characters CommonMark allows to be
// backslash-escaped. A backslash before any of these denotes the literal
// character, so the backslash is dropped.
const mdPunct = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

// unescapeMarkdown reverses CommonMark backslash escaping: `\#`→`#`, `\[`→`[`,
// `\*`→`*`, and so on. markdown.new's degraded conversion path (observed as
// method "Cloudflare Workers AI") escapes almost every punctuation character,
// which otherwise defeats the structural cleanup below: an escaped `\#` heading,
// `\*` list bullet, or `\[text\](url)` link would not match the regexes and
// would leak into the reader verbatim. A backslash not followed by ASCII
// punctuation (e.g. a hard line break `\` at end of line) is left intact.
//
// It operates on bytes: every relevant character (backslash and the punctuation
// set) is single-byte ASCII, and multi-byte UTF-8 sequences pass through
// untouched because their bytes are all >= 0x80.
func unescapeMarkdown(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && strings.IndexByte(mdPunct, s[i+1]) >= 0 {
			b.WriteByte(s[i+1])
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// markdownNewPlaceholderTitle is the generic title markdown.new returns from
// its degraded conversion path when it cannot determine a real one.
const markdownNewPlaceholderTitle = "Converted Content"

// isPlaceholderTitle reports whether t is unusable as an article title: empty,
// or the known markdown.new placeholder.
func isPlaceholderTitle(t string) bool {
	return t == "" || strings.EqualFold(t, markdownNewPlaceholderTitle)
}

// titleFromMarkdown derives a title from the first Markdown heading in content.
// It is the fallback when markdown.new returns an empty or placeholder title.
//
// The heading line may carry inline list metadata flattened onto it — e.g.
// LessWrong's Markdown API yields "# Title * By author * 33 points * Tag: ..."
// on a single line — so only the text up to the first flattened bullet
// separator (" * ") is kept. Returns "" when no heading is found.
func titleFromMarkdown(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = unescapeMarkdown(line)
		marker := headingRe.FindString(line)
		if marker == "" {
			continue
		}
		title := strings.TrimSpace(line[len(marker):])
		if idx := strings.Index(title, " * "); idx >= 0 {
			title = title[:idx]
		}
		if title = inlineClean(title); title != "" {
			return title
		}
	}
	return ""
}

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
	// Emphasis / strikethrough / inline code markers.
	for _, marker := range []string{"**", "__", "~~", "`", "*"} {
		line = strings.ReplaceAll(line, marker, "")
	}
	// Underscore emphasis (_italic_) is common once markdown.new's escaping is
	// removed, but a bare "_" also appears inside snake_case identifiers we want
	// to keep. Drop only boundary underscores, preserving intraword ones.
	line = stripEmphasisUnderscores(line)
	// Table cell pipes → spaces so adjacent cells don't fuse into one token.
	line = strings.ReplaceAll(line, "|", " ")
	return strings.TrimSpace(line)
}

// stripEmphasisUnderscores removes underscores that act as emphasis delimiters
// while preserving underscores inside identifiers (snake_case). An underscore is
// kept only when it sits between two word characters; otherwise it is dropped.
func stripEmphasisUnderscores(line string) string {
	if !strings.Contains(line, "_") {
		return line
	}
	rs := []rune(line)
	var b strings.Builder
	b.Grow(len(line))
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	for i, r := range rs {
		if r != '_' {
			b.WriteRune(r)
			continue
		}
		var prev, next rune
		if i > 0 {
			prev = rs[i-1]
		}
		if i < len(rs)-1 {
			next = rs[i+1]
		}
		if isWord(prev) && isWord(next) {
			b.WriteRune(r) // intraword underscore (snake_case): keep
		}
	}
	return b.String()
}

// collapseBlankLines trims the document and collapses runs of 3+ newlines to a
// single blank-line separator.
func collapseBlankLines(text string) string {
	return strings.TrimSpace(blankRunRe.ReplaceAllString(text, "\n\n"))
}
