// Package normalize holds the content-normalization step of the fetch stage:
// after an article is extracted (markdown.new or the readability fallback) but
// before it is tokenized, an LLM pass strips leftover navigation, reader chrome,
// cookie/subscribe banners, author bios, comment counts, "Prev/Next story"
// links, and other boilerplate the extractor leaked into the article body.
//
// This package owns the deterministic parts of that step — the default prompt
// template, placeholder rendering, and the fail-open safety guard — while the
// single LLM completion is performed by the llm client (which imports this
// package for the template). Keeping the guard here means the policy that
// decides "did normalization keep enough of the article to trust it?" lives in
// one tested place, independent of the HTTP plumbing.
package normalize

import (
	"strings"
	"unicode"

	"deep-reader/internal/model"
)

// DefaultPromptTemplate is the built-in system-prompt template used when the
// user has not configured a custom one via settings. The {{target_language}}
// placeholder is substituted by RenderSystemPrompt at request time. The output
// is the cleaned article body as plain text; the JSON wrapper is enforced
// separately via the response schema (see llm.Normalize).
const DefaultPromptTemplate = "You are a content-cleaning assistant. You are given the raw text of a web " +
	"article that was extracted from a page and may still contain non-article noise. " +
	"Remove everything that is NOT part of the article's actual prose, including: " +
	"site navigation and menus (\"Skip to content\", \"Minimize to nav\"), reader/text " +
	"settings widgets (\"Text settings\", \"Story text\", font-size/width controls), " +
	"subscribe/paywall prompts (\"Subscribers only\", \"Learn more\"), social and share " +
	"buttons, cookie/consent banners, newsletter sign-ups, author bio blocks, byline/role " +
	"lines repeated outside the article, comment counts and comment sections (\"90 Comments\", " +
	"\"Forum view\", \"Loading comments...\"), and related/previous/next-story links " +
	"(\"Prev story\", \"Next story\"). " +
	"Keep the article's headline, body paragraphs, lists, quotes, and image captions that " +
	"belong to the story. Do NOT translate, summarize, rephrase, shorten, or add anything: " +
	"return the kept text verbatim, preserving wording, order, and paragraph breaks. " +
	"The article will later be translated into {{target_language}} for a language learner, " +
	"so fidelity of the original wording is essential. " +
	"Return ONLY the JSON object matching the provided schema, with the cleaned article text " +
	"in the \"content\" field. No markdown fences, no commentary."

// RenderSystemPrompt resolves the effective normalization system prompt: the
// user's custom template (settings.NormalizePrompt) when set, otherwise
// DefaultPromptTemplate, with {{target_language}} substituted.
func RenderSystemPrompt(settings model.Settings) string {
	template := settings.NormalizePrompt
	if template == "" {
		template = DefaultPromptTemplate
	}
	return strings.ReplaceAll(template, "{{target_language}}", settings.TargetLanguage)
}

// MinKeepRatio is the fraction of the original word count the cleaned text must
// retain for normalization to be trusted. A cleaned result shorter than this is
// treated as the model having over-deleted (or hallucinated an empty body), and
// the original text is kept instead. Boilerplate is a small fraction of a real
// article, so a legitimate cleanup removes well under half the words.
const MinKeepRatio = 0.5

// Apply decides whether to accept the LLM-cleaned text. It returns the text to
// use and whether the cleaned version was accepted. The guard is fail-open: if
// the cleaned text is empty or retains fewer than MinKeepRatio of the original's
// words, the original is returned unchanged so a bad normalization can never
// destroy the article (the worst case degrades to "boilerplate not removed",
// never "article lost").
func Apply(original, cleaned string) (text string, accepted bool) {
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return original, false
	}
	origWords := wordCount(original)
	if origWords == 0 {
		return cleaned, true
	}
	if float64(wordCount(cleaned)) < MinKeepRatio*float64(origWords) {
		return original, false
	}
	return cleaned, true
}

// wordCount counts whitespace-separated word runs, where a word is any run
// containing at least one letter or digit. It mirrors the rough notion of
// "content words" used by the guard; punctuation-only runs do not count.
func wordCount(s string) int {
	n := 0
	inWord := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			inWord = false
			continue
		}
		if !inWord && (unicode.IsLetter(r) || unicode.IsNumber(r)) {
			inWord = true
			n++
		}
	}
	return n
}
