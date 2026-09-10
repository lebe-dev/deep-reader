package comments

import (
	"strings"
	"testing"
)

func TestCommentMarkdown(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "deleted comment renders nothing",
			text: "",
			want: "",
		},
		{
			name: "paragraphs are split on the opening p tag",
			text: "First thought.<p>Second thought.",
			want: "First thought.\n\nSecond thought.",
		},
		{
			name: "emphasis becomes markdown emphasis",
			text: "an <i>emphatic</i> and <b>bold</b> claim",
			want: "an *emphatic* and **bold** claim",
		},
		{
			name: "quoted parent stays a blockquote",
			text: "&gt; <i>the parent said this</i><p>No.",
			want: "> *the parent said this*\n\nNo.",
		},
		{
			name: "links keep their label",
			text: `see <a href="https://example.org/paper" rel="nofollow">this paper</a>`,
			want: "see [this paper](https://example.org/paper)",
		},
		{
			name: "a link without a label falls back to the host",
			text: `<a href="https://www.example.org/paper?utm=1"></a>`,
			want: "[example.org](https://www.example.org/paper?utm=1)",
		},
		{
			name: "a bare link is labelled with its host, not tokenized as a URL",
			text: `<a href="https://en.wikipedia.org/wiki/Flow_(psychology)" rel="nofollow">https://en.wikipedia.org/wiki/Flow_(psycho...</a>`,
			want: "[en.wikipedia.org](https://en.wikipedia.org/wiki/Flow_(psychology))",
		},
		{
			name: "code blocks become fences with their source decoded",
			text: "look:<pre><code>if x &lt; 3 {\n  return x\n}\n</code></pre>",
			want: "look:\n\n```\nif x < 3 {\n  return x\n}\n```",
		},
		{
			name: "escaped markup typed by a commenter is not treated as markup",
			text: "write &lt;p&gt; to break a paragraph",
			want: "write <p> to break a paragraph",
		},
		{
			name: "entities are decoded",
			text: "I don&#x27;t agree &amp; here is why",
			want: "I don't agree & here is why",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := commentMarkdown(tc.text); got != tc.want {
				t.Fatalf("commentMarkdown()\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestRenderThread(t *testing.T) {
	item := loadThreadFixture(t)

	text, count := renderThread(item)

	if count != 11 {
		t.Fatalf("comment count: got %d, want 11 (the deleted node is not counted)", count)
	}

	wants := []string{
		// The linked page is offered as a link, never fetched.
		"Link: [example.com/passion](https://example.com/passion)",
		// The story's own text sits above the rule that starts the discussion.
		"When AI makes results readily available, why do I lose my passion?\n\n---",
		// A top-level comment is unquoted; its reply is one quote level deeper,
		// and every author line is an h4 whatever the depth.
		"#### alice\n",
		"> #### bob\n",
		// Formatting survives the conversion, quote markers and all.
		"*emphasis*",
		"[paper](https://example.org/paper)",
		"> > *First thought.*",
		"> ```\n>   if x < 3 {\n>     return x\n>   }\n> ```",
	}
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("rendered thread is missing %q\n---\n%s", want, text)
		}
	}

	if strings.Contains(text, "unknown") {
		t.Errorf("the deleted comment should not get a heading of its own:\n%s", text)
	}
	// The reply to the deleted comment keeps the deleted parent's depth (two
	// quote levels), rather than being pushed a level deeper under a comment
	// that is not there.
	if !strings.Contains(text, "> > #### carol") {
		t.Errorf("orphaned reply should keep its deleted parent's depth:\n%s", text)
	}

	// A blank line inside a comment keeps the quote markers, otherwise the quote
	// ends there and every reply below detaches from the comment it answers.
	if strings.Contains(text, "> #### bob\n\n") {
		t.Errorf("an unquoted blank line inside a comment breaks the nesting:\n%s", text)
	}

	// Indentation is capped; past the cap the author line carries chevrons.
	if !strings.Contains(text, "> > > > > > #### d6\n") {
		t.Errorf("the last reply within the cap should carry no chevron:\n%s", text)
	}
	if !strings.Contains(text, "> > > > > > #### d7 ›\n") {
		t.Errorf("a reply past the depth cap should be marked with a chevron:\n%s", text)
	}
	if strings.Contains(text, "> > > > > > > ") {
		t.Errorf("nesting deeper than the cap emitted:\n%s", text)
	}
}

func TestThreadTitle(t *testing.T) {
	cases := []struct {
		name string
		item *hnItem
		want string
	}{
		{
			name: "story title is used verbatim",
			item: &hnItem{Type: "story", Title: "Why do I lose my passion?"},
			want: "Why do I lose my passion?",
		},
		{
			name: "story title entities are decoded",
			item: &hnItem{Type: "story", Title: "Rust &amp; Go"},
			want: "Rust & Go",
		},
		{
			name: "a comment permalink names its author",
			item: &hnItem{Type: "comment", Author: "alice"},
			want: "Comment by alice on Hacker News",
		},
		{
			name: "an unattributed item falls back to a generic title",
			item: &hnItem{Type: "comment"},
			want: "Hacker News discussion",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := threadTitle(tc.item); got != tc.want {
				t.Fatalf("threadTitle(): got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderThreadForCommentPermalink(t *testing.T) {
	item := &hnItem{
		Type:   "comment",
		Author: "alice",
		Text:   "The shared comment.",
		Children: []*hnItem{
			{Type: "comment", Author: "bob", Text: "A reply to it."},
		},
	}

	text, count := renderThread(item)

	if count != 2 {
		t.Fatalf("comment count: got %d, want 2", count)
	}
	if !strings.HasPrefix(text, "#### alice") {
		t.Errorf("the linked comment should open the text as a top-level comment:\n%s", text)
	}
	if !strings.Contains(text, "> #### bob") {
		t.Errorf("its replies should be nested under it:\n%s", text)
	}
	if strings.Contains(text, "---") {
		t.Errorf("a comment permalink has no story header:\n%s", text)
	}
}
