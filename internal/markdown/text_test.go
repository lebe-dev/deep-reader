package markdown

import "testing"

func TestMarkdownToText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "heading markers stripped",
			in:   "# Title\n\n## Subtitle\n\nBody text.",
			want: "Title\n\nSubtitle\n\nBody text.",
		},
		{
			name: "inline link keeps text drops url",
			in:   "See [the docs](https://example.com/docs) for more.",
			want: "See the docs for more.",
		},
		{
			name: "image removed entirely",
			in:   "Intro ![alt text](https://example.com/img.png) outro.",
			want: "Intro  outro.",
		},
		{
			name: "fenced code block dropped",
			in:   "Before.\n\n```go\nfmt.Println(\"x\")\n```\n\nAfter.",
			want: "Before.\n\nAfter.",
		},
		{
			name: "list markers stripped",
			in:   "- first\n- second\n1. third",
			want: "first\nsecond\nthird",
		},
		{
			name: "blockquote markers stripped",
			in:   "> quoted line\n> > nested",
			want: "quoted line\nnested",
		},
		{
			name: "emphasis markers removed",
			in:   "This is **bold** and `code` and ~~strike~~.",
			want: "This is bold and code and strike.",
		},
		{
			name: "horizontal rule dropped",
			in:   "Above\n\n---\n\nBelow",
			want: "Above\n\nBelow",
		},
		{
			name: "yaml frontmatter stripped",
			in:   "---\ntitle: Hello\nauthor: Jane\n---\nActual content.",
			want: "Actual content.",
		},
		{
			name: "table delimiter row dropped and pipes spaced",
			in:   "| A | B |\n| --- | --- |\n| 1 | 2 |",
			want: "A   B\n1   2",
		},
		{
			name: "snake_case preserved",
			in:   "Call the_function now.",
			want: "Call the_function now.",
		},
		{
			name: "escaped heading unescaped then stripped",
			in:   "\\# Hello world\n\nBody text.",
			want: "Hello world\n\nBody text.",
		},
		{
			name: "escaped inline link drops url keeps text",
			in:   `See \[the docs\](https://example.com) here.`,
			want: "See the docs here.",
		},
		{
			name: "escaped emphasis and brackets cleaned",
			in:   `\_Hello\_ and \[note\]`,
			want: "Hello and [note]",
		},
		{
			name: "flattened escaped list bullets cleaned",
			in:   `\# Title \* By author \* 33 points`,
			want: "Title  By author  33 points",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := markdownToText(tt.in)
			if got != tt.want {
				t.Errorf("markdownToText()\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestUnescapeMarkdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "no backslash is unchanged", in: "plain text", want: "plain text"},
		{name: "escaped punctuation unescaped", in: `\#\*\[\]\_`, want: "#*[]_"},
		{name: "backslash before letter kept", in: `\nope`, want: `\nope`},
		{name: "double backslash collapses to one", in: `a\\b`, want: `a\b`},
		{name: "backslash before unicode kept (not ASCII punct)", in: `\“quote`, want: `\“quote`},
		{name: "unicode content passes through", in: "café — naïve", want: "café — naïve"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := unescapeMarkdown(tt.in); got != tt.want {
				t.Errorf("unescapeMarkdown(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestTitleFromMarkdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "plain heading",
			content: "# Real Title\n\nBody.",
			want:    "Real Title",
		},
		{
			name:    "escaped heading with flattened metadata",
			content: `\# Hell Must Be Destroyed \* By \[algekalipso\](/users/algekalipso) \* 33 points`,
			want:    "Hell Must Be Destroyed",
		},
		{
			name:    "no heading returns empty",
			content: "Just a paragraph with no heading.",
			want:    "",
		},
		{
			name:    "first heading wins",
			content: "# First\n\n## Second",
			want:    "First",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := titleFromMarkdown(tt.content); got != tt.want {
				t.Errorf("titleFromMarkdown() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsPlaceholderTitle(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"":                  true,
		"Converted Content": true,
		"converted content": true,
		"Real Title":        false,
	}
	for in, want := range cases {
		if got := isPlaceholderTitle(in); got != want {
			t.Errorf("isPlaceholderTitle(%q) = %v, want %v", in, got, want)
		}
	}
}
