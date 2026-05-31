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
