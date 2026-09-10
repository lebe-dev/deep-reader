package publish

import (
	"strings"
	"testing"

	"deep-reader/internal/model"
	"deep-reader/internal/tokenize"
)

// payload builds an ArticlePayload from source text plus sentence translations
// expressed as token ranges, so tests read as "these tokens carry this
// translation" rather than as byte arithmetic.
func payload(text string, sentences ...model.Sentence) *model.ArticlePayload {
	return &model.ArticlePayload{
		OriginalText: text,
		Tokens:       tokenize.Tokenize(text),
		Enrichment:   &model.Enrichment{Sentences: sentences},
	}
}

// markdownPayload is payload for a Markdown article — the format a comment
// thread is stored in (see internal/comments).
func markdownPayload(text string, sentences ...model.Sentence) *model.ArticlePayload {
	p := payload(text, sentences...)
	p.ContentFormat = model.ContentFormatMarkdown
	return p
}

// sentenceFor builds a translation for the token range covering want inside the
// payload's text, so a test names the words it translates instead of counting
// token indices.
func sentenceFor(t *testing.T, p *model.ArticlePayload, want, translation string) model.Sentence {
	t.Helper()
	at := strings.Index(p.OriginalText, want)
	if at < 0 {
		t.Fatalf("%q does not occur in the source text", want)
	}
	start, end := -1, -1
	for _, tok := range p.Tokens {
		if tok.Start >= at && tok.End <= at+len(want) {
			if start < 0 {
				start = tok.Index
			}
			end = tok.Index
		}
	}
	if start < 0 {
		t.Fatalf("no tokens cover %q", want)
	}
	return model.Sentence{StartIndex: start, EndIndex: end, Translation: translation}
}

// flatten renders the node tree as one string per node: "kind[depth] segments",
// with a quote level rendered as its own line followed by its children, which is
// compact enough to assert on directly.
func flatten(nodes []Node) []string {
	return flattenAt(nodes, 0)
}

func flattenAt(nodes []Node, depth int) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n.Kind == "quote" {
			label := "quote"
			if n.Comment {
				label = "comment"
			}
			out = append(out, indent(depth)+label)
			out = append(out, flattenAt(n.Children, depth+1)...)
			continue
		}
		parts := make([]string, 0, len(n.Segments))
		for _, s := range n.Segments {
			kind := "orig"
			if s.Translated {
				kind = "ru"
			}
			parts = append(parts, kind+":"+s.Text)
		}
		prefix := indent(depth)
		if n.Kind != "paragraph" {
			prefix += n.Kind + " "
		}
		out = append(out, prefix+joinWith(parts, " / "))
	}
	return out
}

func indent(depth int) string {
	out := ""
	for range depth {
		out += "> "
	}
	return out
}

func joinWith(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

func assertBlocks(t *testing.T, got []Node, want []string) {
	t.Helper()
	flat := flatten(got)
	if len(flat) != len(want) {
		t.Fatalf("node count = %d (%v), want %d (%v)", len(flat), flat, len(want), want)
	}
	for i := range want {
		if flat[i] != want[i] {
			t.Errorf("node %d = %q, want %q", i, flat[i], want[i])
		}
	}
}

func TestBlocksRendersSentenceTranslationsInSourceOrder(t *testing.T) {
	text := "First one. Second one."
	toks := tokenize.Tokenize(text)
	// "Second one." is the tail of the token stream; "First one." the head.
	mid := len(toks) / 2

	// Deliberately out of order: the renderer must sort by source position.
	p := payload(text,
		model.Sentence{StartIndex: mid, EndIndex: len(toks) - 1, Translation: "Второе."},
		model.Sentence{StartIndex: 0, EndIndex: mid - 1, Translation: "Первое."},
	)

	assertBlocks(t, Nodes(p), []string{"ru:Первое. / ru:Второе."})
}

func TestBlocksSplitsParagraphsOnBlankLines(t *testing.T) {
	text := "First one.\n\nSecond one."
	toks := tokenize.Tokenize(text)
	mid := len(toks) / 2

	p := payload(text,
		model.Sentence{StartIndex: 0, EndIndex: mid - 1, Translation: "Первое."},
		model.Sentence{StartIndex: mid, EndIndex: len(toks) - 1, Translation: "Второе."},
	)

	assertBlocks(t, Nodes(p), []string{"ru:Первое.", "ru:Второе."})
}

func TestBlocksKeepsUncoveredTextInTheOriginal(t *testing.T) {
	// Only the first sentence is translated; the second must survive verbatim
	// rather than vanish from the published page.
	text := "First one. Second one."
	toks := tokenize.Tokenize(text)
	mid := len(toks) / 2

	p := payload(text, model.Sentence{StartIndex: 0, EndIndex: mid - 1, Translation: "Первое."})

	assertBlocks(t, Nodes(p), []string{"ru:Первое. / orig:Second one."})
}

func TestBlocksKeepsTextBeforeTheFirstTranslation(t *testing.T) {
	text := "Untranslated lead. Translated tail."
	toks := tokenize.Tokenize(text)
	mid := len(toks) / 2

	p := payload(text, model.Sentence{StartIndex: mid, EndIndex: len(toks) - 1, Translation: "Переведённый хвост."})

	assertBlocks(t, Nodes(p), []string{"orig:Untranslated lead. / ru:Переведённый хвост."})
}

func TestBlocksDropsOverlappingAndInvalidSpans(t *testing.T) {
	text := "Only sentence here."
	toks := tokenize.Tokenize(text)
	last := len(toks) - 1

	p := payload(text,
		model.Sentence{StartIndex: 0, EndIndex: last, Translation: "Единственное предложение."},
		// Overlaps the span above — including it would print the text twice.
		model.Sentence{StartIndex: 0, EndIndex: last, Translation: "Дубликат."},
		// Out of range: the enrichment references a token that does not exist.
		model.Sentence{StartIndex: last + 5, EndIndex: last + 9, Translation: "Мусор."},
		// Inverted span.
		model.Sentence{StartIndex: 2, EndIndex: 1, Translation: "Инверсия."},
		// Empty translation.
		model.Sentence{StartIndex: 0, EndIndex: last},
	)

	assertBlocks(t, Nodes(p), []string{"ru:Единственное предложение."})
}

func TestBlocksWithoutEnrichmentFallsBackToTheOriginal(t *testing.T) {
	p := &model.ArticlePayload{
		OriginalText: "Lead paragraph.\n\nSecond paragraph.",
		Tokens:       tokenize.Tokenize("Lead paragraph.\n\nSecond paragraph."),
	}

	assertBlocks(t, Nodes(p), []string{"orig:Lead paragraph.", "orig:Second paragraph."})
}

func TestBlocksOfEmptyPayloadIsEmpty(t *testing.T) {
	if got := Nodes(&model.ArticlePayload{}); len(got) != 0 {
		t.Fatalf("Nodes of an empty payload = %v, want none", got)
	}
	if got := Nodes(nil); len(got) != 0 {
		t.Fatalf("Nodes(nil) = %v, want none", got)
	}
}

// ── Comment threads ──────────────────────────────────────────────────────────

// thread is the shape internal/comments stores: an author heading per comment,
// one blockquote level per reply.
const thread = "Link: [example.com](https://example.com)\n" +
	"\n" +
	"Story text.\n" +
	"\n" +
	"---\n" +
	"\n" +
	"#### alice\n" +
	"\n" +
	"Root comment.\n" +
	"\n" +
	"> #### bob\n" +
	">\n" +
	"> First reply.\n" +
	">\n" +
	"> > #### carol\n" +
	"> >\n" +
	"> > Nested reply.\n" +
	">\n" +
	"> #### dave\n" +
	">\n" +
	"> Second reply.\n" +
	"\n" +
	"#### erin\n" +
	"\n" +
	"Another root comment.\n"

func TestNodesNestsACommentThread(t *testing.T) {
	p := markdownPayload(thread)

	assertBlocks(t, Nodes(p), []string{
		// Inline markers are stripped: the link keeps its label, the URL goes.
		"orig:Link: example.com",
		"orig:Story text.",
		"rule ",
		"heading orig:alice",
		"orig:Root comment.",
		// bob's reply and dave's reply answer alice, so they share one level;
		// carol answers bob and sits one level deeper, inside it.
		"comment",
		"> heading orig:bob",
		"> orig:First reply.",
		"> comment",
		"> > heading orig:carol",
		"> > orig:Nested reply.",
		"> heading orig:dave",
		"> orig:Second reply.",
		// The unquoted blank line closed the thread back to the top level.
		"heading orig:erin",
		"orig:Another root comment.",
	})
}

func TestNodesKeepsMarkersOutOfTheText(t *testing.T) {
	// The quote markers and heading hashes encode the structure; printing them
	// as words is exactly the failure the tree rendering exists to fix.
	p := markdownPayload("> #### bob\n>\n> A reply.\n>\n> > #### carol\n> >\n> > A nested one.\n")

	for _, text := range segmentTexts(Nodes(p)) {
		if strings.ContainsAny(text, ">#") {
			t.Errorf("segment %q still carries a Markdown marker", text)
		}
	}
}

// segmentTexts collects the text of every segment in the tree.
func segmentTexts(nodes []Node) []string {
	var out []string
	for _, n := range nodes {
		for _, s := range n.Segments {
			out = append(out, s.Text)
		}
		out = append(out, segmentTexts(n.Children)...)
	}
	return out
}

func TestNodesPlacesTranslationsInTheCommentTheyBelongTo(t *testing.T) {
	p := markdownPayload(thread)
	p.Enrichment = &model.Enrichment{Sentences: []model.Sentence{
		sentenceFor(t, p, "Nested reply.", "Вложенный ответ."),
		sentenceFor(t, p, "Root comment.", "Корневой комментарий."),
	}}

	flat := flatten(Nodes(p))
	if !contains(flat, "orig:Root comment.") && !contains(flat, "ru:Корневой комментарий.") {
		t.Fatalf("the root translation is missing: %v", flat)
	}
	if !contains(flat, "ru:Корневой комментарий.") {
		t.Errorf("the root comment should carry its translation: %v", flat)
	}
	if !contains(flat, "> > ru:Вложенный ответ.") {
		t.Errorf("the nested translation should stay two levels deep: %v", flat)
	}
}

func TestNodesRendersAQuotationWithoutAnAuthorAsAQuote(t *testing.T) {
	// A commenter quoting the parent: a quote level with no author heading.
	p := markdownPayload("#### bob\n\n> *what alice said*\n\nMy answer.\n")

	assertBlocks(t, Nodes(p), []string{
		"heading orig:bob",
		"quote",
		"> orig:what alice said",
		"orig:My answer.",
	})
}

func TestNodesKeepsACodeBlockVerbatim(t *testing.T) {
	p := markdownPayload("> #### bob\n>\n> ```\n> if x < 3 {\n>   return x\n> }\n> ```\n")

	assertBlocks(t, Nodes(p), []string{
		"comment",
		"> heading orig:bob",
		"> code orig:if x < 3 {\n  return x\n}",
	})
}

func TestNodesOfAPlainArticleStaysFlat(t *testing.T) {
	// A plain article must not gain structure from characters that only mean
	// something in Markdown.
	p := payload("> not a quote here\n\n#### not a heading either\n")

	assertBlocks(t, Nodes(p), []string{
		"orig:> not a quote here",
		"orig:#### not a heading either",
	})
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestNodesStripsInlineMarkersFromUntranslatedText(t *testing.T) {
	// Emphasis and link syntax are structure the page cannot render inline, so
	// the markers are dropped rather than printed as words. Code keeps its own.
	p := markdownPayload("A *loud* [label](https://example.com/x) claim.\n\n```\nweight *= 2\n```\n")

	assertBlocks(t, Nodes(p), []string{
		"orig:A loud label claim.",
		"code orig:weight *= 2",
	})
}
