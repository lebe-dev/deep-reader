package markdown

import "testing"

func TestIsMarkdown(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "plain prose",
			text: "This is an ordinary paragraph of prose. It has sentences, " +
				"commas, and even a 3.14 number, but no Markdown structure at all.",
			want: false,
		},
		{
			name: "plain prose with a single asterisk",
			text: "The result was good * but not perfect, he said.",
			want: false,
		},
		{
			name: "single list-looking line is not enough",
			text: "We met at 5 - it was late already and everyone had gone home.",
			want: false,
		},
		{
			name: "atx heading",
			text: "# Introduction\n\nSome body text follows the heading here.",
			want: true,
		},
		{
			name: "heading deeper than h1",
			text: "Intro line.\n\n### Details\n\nMore prose under the subheading.",
			want: true,
		},
		{
			name: "fenced code block",
			text: "Run this:\n\n```go\nfmt.Println(\"hi\")\n```\n\nDone.",
			want: true,
		},
		{
			name: "blockquote",
			text: "As they said:\n\n> To be or not to be.\n\nThat is the question.",
			want: true,
		},
		{
			name: "bullet list with multiple items",
			text: "Shopping list:\n\n- milk\n- eggs\n- bread",
			want: true,
		},
		{
			name: "ordered list with multiple items",
			text: "Steps:\n\n1. First do this.\n2. Then do that.\n3. Finally finish.",
			want: true,
		},
		{
			name: "table",
			text: "| Name | Age |\n| --- | --- |\n| Ann | 30 |\n| Bob | 25 |",
			want: true,
		},
		{
			name: "thematic break",
			text: "Above the line.\n\n---\n\nBelow the line.",
			want: true,
		},
		{
			name: "multiple inline emphasis and code",
			text: "Use **bold**, some *italic*, and `code` to make the point clearly.",
			want: true,
		},
		{
			name: "single bold is not enough",
			text: "Only one **emphasised** word in an otherwise plain paragraph here.",
			want: false,
		},
		{
			name: "empty",
			text: "",
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsMarkdown(tc.text); got != tc.want {
				t.Errorf("IsMarkdown(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestDetectFormat(t *testing.T) {
	if got := DetectFormat("# Heading\n\nbody"); got != "markdown" {
		t.Errorf("DetectFormat(markdown) = %q, want %q", got, "markdown")
	}
	if got := DetectFormat("just plain prose without any structure here"); got != "plain" {
		t.Errorf("DetectFormat(plain) = %q, want %q", got, "plain")
	}
}
