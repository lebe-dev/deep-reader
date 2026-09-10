package publish

import (
	"bytes"
	"fmt"
	"html/template"
	"time"
)

// Page is everything the generated HTML file needs. It is fully resolved at
// publish time — nothing here is looked up when the page is served.
type Page struct {
	// Title and Description are the user-editable Open Graph metadata; Title
	// doubles as the document title and the on-page heading.
	Title       string
	Description string
	// URL is the absolute link the page is published under (og:url).
	URL string
	// Author, SourceURL and SourceDomain attribute the original article. They
	// are omitted from the byline when empty.
	Author       string
	SourceURL    string
	SourceDomain string
	// Lang is the BCP 47 tag of the translated text (Settings.TargetLanguage),
	// used for the document lang attribute.
	Lang string
	// SourceLang is the BCP 47 tag of the original article, applied to the
	// untranslated fallback runs so assistive tech switches pronunciation.
	SourceLang string
	// PublishedAt stamps the page's article:published_time.
	PublishedAt time.Time
	// Nodes is the rendered content tree (see Nodes): a flat list of paragraphs
	// for an article, a nested one for a comment thread.
	Nodes []Node
}

// nodeScope is what the recursive content template is executed with: the nodes
// of one level plus the source language, which the untranslated runs need for
// their lang attribute and which a nested template cannot reach on its own.
type nodeScope struct {
	Nodes      []Node
	SourceLang string
}

// pageFuncs lets the template build the scope for the next level down.
var pageFuncs = template.FuncMap{
	"scope": func(nodes []Node, sourceLang string) nodeScope {
		return nodeScope{Nodes: nodes, SourceLang: sourceLang}
	},
}

// pageTemplate is the whole public page: metadata, styles and content in one
// self-contained file with no external requests. It follows the reader's own
// look — a single narrow measure of serif text, light and dark via
// prefers-color-scheme.
var pageTemplate = template.Must(template.New("page").Funcs(pageFuncs).Parse(`<!doctype html>
<html lang="{{ .Lang }}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{ .Title }}</title>
<meta name="description" content="{{ .Description }}">
<meta name="robots" content="noindex, nofollow">
<meta property="og:type" content="article">
<meta property="og:title" content="{{ .Title }}">
<meta property="og:description" content="{{ .Description }}">
{{- if .URL }}
<meta property="og:url" content="{{ .URL }}">
{{- end }}
<meta property="og:site_name" content="Deep Reader">
<meta property="og:locale" content="{{ .Lang }}">
{{- if not .PublishedAt.IsZero }}
<meta property="article:published_time" content="{{ .PublishedAt.UTC.Format "2006-01-02T15:04:05Z" }}">
{{- end }}
<meta name="twitter:card" content="summary">
<meta name="twitter:title" content="{{ .Title }}">
<meta name="twitter:description" content="{{ .Description }}">
<style>
:root {
  color-scheme: light dark;
  --bg: #fbfaf8;
  --fg: #1b1a17;
  --muted: #6b6862;
  --rule: #e3dfd7;
  --accent: #8a5a2b;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #16151a;
    --fg: #e8e6e1;
    --muted: #9a958c;
    --rule: #2f2d34;
    --accent: #d0a06a;
  }
}
* { box-sizing: border-box; }
body {
  margin: 0;
  background: var(--bg);
  color: var(--fg);
  font-family: Georgia, "Iowan Old Style", "Times New Roman", serif;
  font-size: 1.125rem;
  line-height: 1.7;
  -webkit-text-size-adjust: 100%;
}
main { max-width: 38rem; margin: 0 auto; padding: 3rem 1.25rem 4rem; }
header { border-bottom: 1px solid var(--rule); padding-bottom: 1.5rem; margin-bottom: 2rem; }
h1 { font-size: 1.85rem; line-height: 1.25; margin: 0 0 .75rem; }
.byline { color: var(--muted); font-size: .9rem; line-height: 1.5; font-family: system-ui, -apple-system, "Segoe UI", sans-serif; }
.byline a { color: inherit; }
p { margin: 0 0 1.35rem; }
.orig { color: var(--muted); font-style: italic; }
/* One level of quote nesting. In a comment thread this is one reply level, so
   the rail is the thread line the reader draws too. */
.q { margin: 0 0 1.35rem; padding-left: .9rem; border-left: 2px solid var(--rule); }
.q > *:last-child { margin-bottom: 0; }
/* A quote level that is not a comment is someone quoting something. */
.q.quotation { color: var(--muted); font-style: italic; }
h1, h2, h3 { line-height: 1.3; margin: 2rem 0 .75rem; }
h2 { font-size: 1.4rem; }
h3 { font-size: 1.2rem; }
/* h4-h6 are the author lines of a thread: a label, not a section heading. */
h4, h5, h6 { font-family: system-ui, -apple-system, "Segoe UI", sans-serif; font-size: .85rem; font-weight: 600; color: var(--muted); margin: 0 0 .4rem; }
pre { margin: 0 0 1.35rem; padding: .75rem .9rem; overflow-x: auto; border-radius: .4rem; background: color-mix(in srgb, var(--rule) 45%, transparent); font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: .85rem; line-height: 1.5; }
hr { border: 0; border-top: 1px solid var(--rule); margin: 2rem 0; }
@media (max-width: 480px) { .q { padding-left: .6rem; } }
footer { margin-top: 3rem; padding-top: 1.25rem; border-top: 1px solid var(--rule); color: var(--muted); font-size: .8rem; font-family: system-ui, -apple-system, "Segoe UI", sans-serif; }
footer a { color: var(--accent); }
</style>
</head>
<body>
<main>
<header>
<h1>{{ .Title }}</h1>
{{- if or .Author .SourceDomain }}
<p class="byline">
{{- if .Author }}{{ .Author }}{{ end }}
{{- if and .Author .SourceDomain }} · {{ end }}
{{- if .SourceDomain }}
{{- if .SourceURL }}<a href="{{ .SourceURL }}" rel="noopener nofollow ugc">{{ .SourceDomain }}</a>{{ else }}{{ .SourceDomain }}{{ end }}
{{- end }}
</p>
{{- end }}
</header>
{{- template "nodes" (scope .Nodes .SourceLang) }}
<footer>Translated with <a href="https://github.com/tiny-ops/deep-reader" rel="noopener">Deep Reader</a>.</footer>
</main>
</body>
</html>
{{- define "nodes" }}
{{- $lang := .SourceLang }}
{{- range .Nodes }}
{{- if eq .Kind "quote" }}
<div class="q{{ if not .Comment }} quotation{{ end }}">
{{- template "nodes" (scope .Children $lang) }}
</div>
{{- else if eq .Kind "heading" }}
<h{{ .Level }}>{{ range $i, $s := .Segments }}{{ if $i }} {{ end }}{{ if $s.Translated }}{{ $s.Text }}{{ else }}<span class="orig" lang="{{ $lang }}">{{ $s.Text }}</span>{{ end }}{{ end }}</h{{ .Level }}>
{{- else if eq .Kind "code" }}
<pre><code>{{ range $i, $s := .Segments }}{{ if $i }}
{{ end }}{{ $s.Text }}{{ end }}</code></pre>
{{- else if eq .Kind "rule" }}
<hr>
{{- else }}
<p>
{{- range $i, $s := .Segments }}{{ if $i }} {{ end }}
{{- if $s.Translated }}{{ $s.Text }}{{ else }}<span class="orig" lang="{{ $lang }}">{{ $s.Text }}</span>{{ end }}
{{- end }}
</p>
{{- end }}
{{- end }}
{{- end }}
`))

// notFoundPage is what a reader gets for a link that is unknown, revoked, or
// past its TTL. All three cases answer the same 404 with the same wording: a
// dead link should not reveal whether it ever existed.
var notFoundPage = []byte(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Page not found</title>
<meta name="robots" content="noindex, nofollow">
<style>
:root { color-scheme: light dark; --bg: #fbfaf8; --fg: #1b1a17; --muted: #6b6862; }
@media (prefers-color-scheme: dark) { :root { --bg: #16151a; --fg: #e8e6e1; --muted: #9a958c; } }
body { margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center; background: var(--bg); color: var(--fg); font-family: system-ui, -apple-system, "Segoe UI", sans-serif; text-align: center; padding: 1.5rem; }
h1 { font-size: 1.5rem; margin: 0 0 .5rem; }
p { color: var(--muted); margin: 0; line-height: 1.6; }
</style>
</head>
<body>
<main>
<h1>Page not found</h1>
<p>This link is no longer available — it may have expired or been removed.</p>
</main>
</body>
</html>
`)

// NotFoundPage returns the standalone 404 document served for links that no
// longer resolve.
func NotFoundPage() []byte { return notFoundPage }

// RenderPage produces the complete HTML document for a published article.
func RenderPage(p Page) ([]byte, error) {
	if p.Lang == "" {
		p.Lang = "ru"
	}
	if p.SourceLang == "" {
		p.SourceLang = "en"
	}

	var buf bytes.Buffer
	if err := pageTemplate.Execute(&buf, p); err != nil {
		return nil, fmt.Errorf("rendering public page: %w", err)
	}
	return buf.Bytes(), nil
}
