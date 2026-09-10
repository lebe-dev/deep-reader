package comments

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// The thread is rendered as Markdown (model.ContentFormatMarkdown), not as the
// flat prose the article extractors produce, because the structure IS the
// content: who replied to whom is most of what a discussion means. The reader
// already renders Markdown blocks while keeping every word interactive, so
// nesting costs nothing on the client.
//
// Depth is encoded as blockquote nesting: a top-level comment is unquoted, a
// reply carries one ">" marker, its reply two, and so on — the shape a threaded
// discussion already has in Markdown. Heading level is NOT used for depth: it
// stops at h6, and a heading only changes the size of the author line, which
// leaves a deep thread looking like a flat list of names (the reader indents
// each quote level instead, drawing the reply lines the structure implies).
//
// The author of every comment is an h4 regardless of depth, so the name reads
// as a label rather than as a section heading that shrinks the deeper the
// conversation goes.
const (
	// authorHeading is the heading marker of the author line of a comment.
	authorHeading = "####"
	// maxQuoteDepth caps the nesting markers. Beyond it the indentation would
	// leave no width for the text on a phone, so deeper replies stay at the cap
	// and get a chevron per extra level in the author line, keeping a long
	// back-and-forth readable instead of unreadably narrow.
	maxQuoteDepth = 6
)

// renderThread renders a fetched item (a story with its comments, or a single
// comment permalink with its replies) as Markdown, and returns the number of
// comments it emitted.
//
// Only the discussion is rendered: the story's linked page is left as a link,
// never fetched — pulling it would silently turn one library card into two
// different texts.
func renderThread(item *hnItem) (string, int) {
	if item == nil {
		return "", 0
	}

	var b strings.Builder
	writeStoryHeader(&b, item)

	var thread []renderedComment
	if item.Type == "comment" {
		// A comment permalink: the item itself is the root of what the user
		// shared, so it is rendered as the first comment rather than as a header.
		thread = collectComments(nil, item, 0)
	} else {
		for _, child := range item.Children {
			thread = collectComments(thread, child, 0)
		}
	}

	for i, c := range thread {
		if i > 0 {
			// The blank line between two comments belongs to the shallower of the
			// two: quoted at the depth they still share, it keeps the outer quote
			// open for a reply, and unquoted it closes the thread back to the
			// level the next comment starts at.
			b.WriteString(blankLine(min(thread[i-1].depth, c.depth)))
		}
		prefix := quotePrefix(c.depth)
		for _, line := range c.lines {
			if line == "" {
				b.WriteString(blankLine(c.depth))
				continue
			}
			b.WriteString(prefix + line + "\n")
		}
	}

	return strings.TrimSpace(b.String()) + "\n", len(thread)
}

// writeStoryHeader emits the story's own block: the link to the page under
// discussion (when the story links out) and the submitted text (when it is an
// Ask/Show HN self-post), followed by a rule separating it from the comments.
func writeStoryHeader(b *strings.Builder, item *hnItem) {
	if item.Type == "comment" {
		return
	}

	wrote := false
	if item.URL != "" {
		fmt.Fprintf(b, "Link: [%s](%s)\n\n", linkLabel(item.URL), item.URL)
		wrote = true
	}
	if body := commentMarkdown(item.Text); body != "" {
		b.WriteString(body)
		b.WriteString("\n\n")
		wrote = true
	}
	if wrote {
		b.WriteString("---\n\n")
	}
}

// renderedComment is one comment ready to be written: its nesting depth and its
// Markdown lines without the quote markers that depth implies.
type renderedComment struct {
	depth int
	lines []string
}

// collectComments appends item and, recursively, its replies to out in reading
// order. A deleted comment (no text) contributes nothing but its replies are
// kept at its own depth, so a surviving sub-thread is not silently dropped
// along with the moderated parent.
func collectComments(out []renderedComment, item *hnItem, depth int) []renderedComment {
	if item == nil {
		return out
	}

	childDepth := depth
	if body := commentMarkdown(item.Text); body != "" {
		lines := []string{authorHeading + " " + authorLabel(item, depth), ""}
		lines = append(lines, strings.Split(body, "\n")...)
		out = append(out, renderedComment{depth: depth, lines: lines})
		childDepth = depth + 1
	}

	for _, child := range item.Children {
		out = collectComments(out, child, childDepth)
	}
	return out
}

// blankLine is the separator line at a given nesting depth: empty at the top
// level, and the bare quote markers inside a thread — an unquoted blank line
// would end the quote there and detach every reply below it from the comment it
// answers.
func blankLine(depth int) string {
	return strings.TrimRight(quotePrefix(depth), " ") + "\n"
}

// quotePrefix returns the blockquote markers for a comment at depth, clamped to
// maxQuoteDepth.
func quotePrefix(depth int) string {
	return strings.Repeat("> ", min(depth, maxQuoteDepth))
}

// authorLabel is the heading text of a comment: the author, plus one chevron
// per nesting level past the indentation cap. Chevrons are punctuation, so the
// tokenizer skips them and they cost no tokens.
func authorLabel(item *hnItem, depth int) string {
	author := item.Author
	if author == "" {
		author = "unknown"
	}
	if extra := depth - maxQuoteDepth; extra > 0 {
		return author + " " + strings.Repeat("›", extra)
	}
	return author
}

// threadTitle is the article title: the story title, or a description of whose
// comment was linked when the URL points at a single comment.
func threadTitle(item *hnItem) string {
	if item == nil {
		return "Hacker News discussion"
	}
	if title := strings.TrimSpace(html.UnescapeString(item.Title)); title != "" {
		return title
	}
	if item.Author != "" {
		return "Comment by " + item.Author + " on Hacker News"
	}
	return "Hacker News discussion"
}

// linkHost is the display label for a bare link — one a commenter pasted without
// writing any text over it, which HN renders with the URL itself as the label.
// The full URL would otherwise be tokenized into junk words ("https", "en",
// "wikipedia", "org", …) that pollute the token stream the reader taps on and
// the vocabulary cache behind it, so only the host survives: enough to see where
// the link goes, at the cost of two ordinary words.
func linkHost(rawURL string) string {
	host := linkLabel(rawURL)
	if idx := strings.IndexAny(host, "/"); idx >= 0 {
		host = host[:idx]
	}
	return host
}

// looksLikeURL reports whether a link label is the URL itself rather than words
// a commenter wrote. HN truncates a long bare link with an ellipsis, so the
// label is not necessarily a valid URL — matching the scheme is enough.
func looksLikeURL(label string) bool {
	lower := strings.ToLower(label)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// linkLabel shortens a URL to host + path for display, so the link line reads
// like a source line instead of a wall of query parameters.
func linkLabel(rawURL string) string {
	label := rawURL
	if idx := strings.Index(label, "://"); idx >= 0 {
		label = label[idx+3:]
	}
	label = strings.TrimPrefix(label, "www.")
	if idx := strings.IndexAny(label, "?#"); idx >= 0 {
		label = label[:idx]
	}
	return strings.TrimSuffix(label, "/")
}

// ---------------------------------------------------------------------------
// Comment HTML → Markdown
// ---------------------------------------------------------------------------

// Hacker News stores comment bodies as a tiny HTML subset: paragraphs opened
// (never closed) with <p>, <i>/<b> emphasis, <a href> links, <pre><code> blocks,
// and HTML-escaped text. Converting it to Markdown — rather than stripping the
// tags — keeps quotes, code and emphasis visible in the reader, and it is the
// same line/regex approach internal/markdown/text.go takes: a full HTML parser
// buys nothing when the input is this constrained.
var (
	preRe    = regexp.MustCompile(`(?is)<pre>\s*<code>(.*?)</code>\s*</pre>`)
	linkRe   = regexp.MustCompile(`(?is)<a\s[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	italicRe = regexp.MustCompile(`(?is)<i>(.*?)</i>`)
	boldRe   = regexp.MustCompile(`(?is)<b>(.*?)</b>`)
	codeRe   = regexp.MustCompile("(?is)<code>(.*?)</code>")
	tagRe    = regexp.MustCompile(`(?is)<[^>]+>`)
	blankRe  = regexp.MustCompile(`\n{3,}`)
)

// codePlaceholder marks where a fenced code block is spliced back in. It uses
// NUL, which cannot occur in the API's JSON strings, so no comment text can
// collide with it.
const codePlaceholder = "\x00code:%d\x00"

// commentMarkdown converts one HN comment body to Markdown. An empty or
// whitespace-only body (a deleted comment) returns "".
func commentMarkdown(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}

	// Code blocks are lifted out first and reinserted last: their content is
	// escaped source, so it must not be run through the inline rules that turn
	// angle brackets and asterisks into Markdown markers.
	var blocks []string
	out := preRe.ReplaceAllStringFunc(text, func(m string) string {
		inner := preRe.FindStringSubmatch(m)[1]
		blocks = append(blocks, "```\n"+strings.Trim(html.UnescapeString(inner), "\n")+"\n```")
		return fmt.Sprintf(codePlaceholder, len(blocks)-1)
	})

	// <p> opens a paragraph and is never closed on HN.
	out = strings.ReplaceAll(out, "<p>", "\n\n")
	out = strings.ReplaceAll(out, "</p>", "")

	out = linkRe.ReplaceAllStringFunc(out, func(m string) string {
		g := linkRe.FindStringSubmatch(m)
		href := html.UnescapeString(g[1])
		label := strings.TrimSpace(html.UnescapeString(tagRe.ReplaceAllString(g[2], "")))
		if label == "" || looksLikeURL(label) {
			label = linkHost(href)
		}
		return "[" + label + "](" + href + ")"
	})

	out = italicRe.ReplaceAllString(out, "*$1*")
	out = boldRe.ReplaceAllString(out, "**$1**")
	out = codeRe.ReplaceAllString(out, "`$1`")
	// Anything left is a tag HN does not normally emit; drop the markup, keep
	// the words.
	out = tagRe.ReplaceAllString(out, "")

	// Entities are decoded only now, so a &lt;p&gt; typed by a commenter is never
	// mistaken for markup above. Links and code blocks were already decoded.
	out = html.UnescapeString(out)

	for i, block := range blocks {
		out = strings.ReplaceAll(out, fmt.Sprintf(codePlaceholder, i), "\n\n"+block+"\n\n")
	}

	return tidyLines(out)
}

// tidyLines trims trailing spaces, collapses runs of blank lines, and drops a
// leading/trailing blank line — the shape the Markdown block parser expects.
func tidyLines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimSpace(blankRe.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}
