// Package llm provides a thin OpenAI-compatible Chat Completions client that
// implements ports.LLMClient. It performs exactly one HTTP request per Enrich
// call; retry / backoff is the caller's responsibility (enrich.Pool).
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"deep-reader/internal/config"
	"deep-reader/internal/model"
	"deep-reader/internal/normalize"
	"deep-reader/internal/ports"
)

// Client is the LLM client. Construct it with New(cfg, opts...).
//
// The connection (base URL, API key, model) is resolved per call: when a
// provider resolver is configured (WithProviderResolver) the active profile
// wins, so a profile edited in the UI takes effect on the next call without a
// restart. The cfg-derived fields below are the fallback used when no resolver
// is set or no profile is configured.
type Client struct {
	resolver   ports.LLMProviderResolver
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// Option customises Client construction.
type Option func(*Client)

// WithProviderResolver wires the active-profile resolver (the store). Without it
// the client uses only its cfg-derived connection.
func WithProviderResolver(r ports.LLMProviderResolver) Option {
	return func(c *Client) { c.resolver = r }
}

// New creates a new Client from cfg. It satisfies ports.LLMClient.
func New(cfg *config.Config, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(cfg.LLMAPIBaseURL, "/"),
		apiKey:  cfg.LLMAPIKey,
		model:   cfg.LLMModel,
		httpClient: &http.Client{
			Timeout: cfg.LLMRequestTimeout,
		},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// conn is the resolved connection for a single call.
type conn struct {
	baseURL string
	apiKey  string
	model   string
	// forceJSONObject, set from the active profile, makes JSON calls request the
	// json_object response format directly, skipping json_schema and its one-shot
	// fallback (see schemaResponseFormat / complete).
	forceJSONObject bool
}

// resolveConn returns the connection for a single call together with a flag
// reporting whether it came from an active provider profile.
//
// When a resolver is wired (the production path, see cmd/server wiring) the
// active UI profile is the SOLE source of the connection — base URL, API key
// and model. We deliberately do NOT fall back to the env-derived cfg values
// here: the LLM_* env vars are only a first-boot seed for the provider table
// (store.seedLLMProvider), never a request-time source. So if the user has no
// usable active profile (e.g. they deleted the last one) the returned conn is
// empty and the call fails clearly, rather than silently resurrecting stale
// env config. The cfg-derived fallback below applies only when no resolver is
// set at all (library/test construction).
func (c *Client) resolveConn(ctx context.Context) (conn, bool) {
	if c.resolver != nil {
		p, err := c.resolver.GetActiveLLMProvider(ctx)
		if err != nil || p.BaseURL == "" {
			return conn{}, false
		}
		return conn{baseURL: strings.TrimRight(p.BaseURL, "/"), apiKey: p.APIKey, model: p.Model, forceJSONObject: p.ForceJSONObject}, true
	}
	return conn{baseURL: c.baseURL, apiKey: c.apiKey, model: c.model}, false
}

// effectiveModel picks the model name for a request given the resolved
// connection and the per-user settings override.
//
// When the connection comes from an active provider profile, that profile is
// the single source of truth for the connection — so its model wins and the
// legacy per-user settings.llm_model only fills in when the profile leaves the
// model blank (e.g. an env-seeded "Default" profile). Without a profile (env /
// cfg fallback) the historical precedence holds: settings.llm_model overrides
// the deployment default so a model can be changed without a redeploy.
func effectiveModel(cn conn, fromProfile bool, settingsModel string) string {
	if fromProfile {
		if cn.model != "" {
			return cn.model
		}
		return settingsModel
	}
	return modelName(cn.model, settingsModel)
}

// ---------------------------------------------------------------------------
// OpenAI-compatible request / response types (unexported).
// ---------------------------------------------------------------------------

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type       string      `json:"type"`
	JSONSchema *jsonSchema `json:"json_schema,omitempty"`
}

type jsonSchema struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict"`
}

// schemaResponseFormat returns the response_format for a structured (JSON) call.
// It prefers json_schema (strict structured outputs) unless forceJSONObject is
// set on the active profile, in which case it asks for json_object directly —
// skipping the json_schema attempt and its one-shot fallback for providers that
// always reject the schema variant (see isSchemaUnsupported).
func schemaResponseFormat(name, schema string, forceJSONObject bool) *responseFormat {
	if forceJSONObject {
		return &responseFormat{Type: "json_object"}
	}
	return &responseFormat{
		Type: "json_schema",
		JSONSchema: &jsonSchema{
			Name:   name,
			Schema: json.RawMessage(schema),
			Strict: true,
		},
	}
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Temperature    float64         `json:"temperature"`
}

type chatChoice struct {
	Message chatMessage `json:"message"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
	Usage   *chatUsage   `json:"usage,omitempty"`
}

// ---------------------------------------------------------------------------
// Prompt construction.
// ---------------------------------------------------------------------------

// enrichmentSchema is the JSON Schema for model.Enrichment, embedded in the
// system prompt when the provider supports response_format json_schema.
const enrichmentSchema = `{
  "type": "object",
  "required": ["difficult_words", "phrases", "sentences", "glossary"],
  "additionalProperties": false,
  "properties": {
    "difficult_words": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["token_index", "lemma", "translation", "cefr_level"],
        "additionalProperties": false,
        "properties": {
          "token_index": {"type": "integer"},
          "lemma":       {"type": "string"},
          "translation": {"type": "string"},
          "cefr_level":  {"type": "string", "enum": ["A2","B1","B2","C1","C2"]}
        }
      }
    },
    "phrases": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["start_index", "end_index", "type", "text", "translation"],
        "additionalProperties": false,
        "properties": {
          "start_index": {"type": "integer"},
          "end_index":   {"type": "integer"},
          "type":        {"type": "string", "enum": ["idiom","phrasal_verb","term"]},
          "text":        {"type": "string"},
          "translation": {"type": "string"}
        }
      }
    },
    "sentences": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["start_index", "end_index", "translation"],
        "additionalProperties": false,
        "properties": {
          "start_index": {"type": "integer"},
          "end_index":   {"type": "integer"},
          "translation": {"type": "string"}
        }
      }
    },
    "glossary": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["term", "definition"],
        "additionalProperties": false,
        "properties": {
          "term":       {"type": "string"},
          "definition": {"type": "string"}
        }
      }
    }
  }
}`

// DefaultEnrichmentPromptTemplate is the built-in system-prompt template used
// when the user has not configured a custom one via settings. The placeholders
// {{enrichment_version}}, {{target_language}}, {{cefr_level}} and
// {{min_difficulty}} are substituted by renderPrompt at request time. The JSON
// schema itself is enforced separately via response_format, not by this text.
const DefaultEnrichmentPromptTemplate = "You are a language-learning assistant (enrichment schema v{{enrichment_version}}).\n" +
	"Your task is to analyse an English article and produce structured annotations " +
	"for a reader whose target translation language is {{target_language}} and whose CEFR proficiency level is {{cefr_level}}.\n\n" +
	"Rules:\n" +
	"1. difficult_words: list every token whose CEFR level is AT OR ABOVE {{min_difficulty}} (the user's min_difficulty_to_highlight). " +
	"Include the token's dictionary lemma, a contextual translation into {{target_language}}, and the CEFR level. " +
	"Use token_index from the input array.\n" +
	"2. phrases: identify idioms, phrasal verbs, and domain terms as contiguous token ranges. " +
	"Provide start_index and end_index (inclusive) from the input array, the phrase type " +
	"(idiom | phrasal_verb | term), the exact phrase text, and a translation/definition into {{target_language}}. " +
	"The text field MUST be the verbatim concatenation of exactly the tokens from start_index to " +
	"end_index (joined by single spaces) and nothing more — start_index and end_index must delimit " +
	"precisely that phrase, not the surrounding sentence. Keep term ranges tight (usually 1–4 tokens).\n" +
	"3. sentences: for every sentence provide start_index and end_index (inclusive) of its " +
	"tokens and a fluent translation into {{target_language}}.\n" +
	"4. glossary: for domain-specific terms that deserve a definition rather than a plain translation, " +
	"add an entry with the English term and an explanation in {{target_language}}.\n" +
	"5. Return ONLY the JSON object matching the provided schema. No markdown, no prose.\n"

// renderPrompt substitutes the supported placeholders in template with the
// per-request settings and enrichment version.
func renderPrompt(template string, settings model.Settings, enrichmentVersion int) string {
	r := strings.NewReplacer(
		"{{enrichment_version}}", strconv.Itoa(enrichmentVersion),
		"{{target_language}}", settings.TargetLanguage,
		"{{cefr_level}}", settings.CEFRLevel,
		"{{min_difficulty}}", settings.MinDifficultyToHighlight,
	)
	return r.Replace(template)
}

// buildPrompt returns the system and user messages for the enrichment call.
// The system prompt comes from the user's configured enrichment prompt
// template, falling back to DefaultEnrichmentPromptTemplate when unset. The
// enrichmentVersion is substituted into the template so that prompt changes can
// be traced via the version number.
func buildPrompt(a *model.Article, settings model.Settings, enrichmentVersion int) (system, user string) {
	system = renderSystemPrompt(settings, enrichmentVersion)
	user = buildUserPrompt(a, a.Tokens)
	return system, user
}

// renderSystemPrompt renders the enrichment system prompt from the user's
// configured template (or the built-in default when unset).
func renderSystemPrompt(settings model.Settings, enrichmentVersion int) string {
	template := settings.EnrichmentPrompt
	if template == "" {
		template = DefaultEnrichmentPromptTemplate
	}
	return renderPrompt(template, settings, enrichmentVersion)
}

// buildUserPrompt renders the user message: the article title, an optional
// summary for whole-article context, and the supplied tokens as an indexed
// (index → text) JSON array.
//
// tokens may be a subset of the article's tokens — the step-wise / incremental
// path sends only the tokens it is annotating instead of the whole article on
// every chunk. The explicit per-token index field is what keeps that subset
// aligned with the full article: the model echoes those same indices back, so
// the references stay valid regardless of which slice was sent.
func buildUserPrompt(a *model.Article, tokens []model.Token) string {
	type indexedToken struct {
		Index int    `json:"index"`
		Text  string `json:"text"`
	}
	idx := make([]indexedToken, len(tokens))
	for i, t := range tokens {
		idx[i] = indexedToken{Index: t.Index, Text: t.Text}
	}
	tokenJSON, _ := json.Marshal(idx)

	var ub strings.Builder
	fmt.Fprintf(&ub, "Article title: %s\n\n", a.Title)
	if strings.TrimSpace(a.Summary) != "" {
		// The summary (produced by the first enrichment step) gives the model
		// whole-article context so per-chunk translations and term choices stay
		// consistent across chunks even though each chunk sees only its own tokens.
		fmt.Fprintf(&ub, "Article summary (for context only, do not annotate it):\n%s\n\n", a.Summary)
	}
	fmt.Fprintf(&ub,
		"Tokenized text (index → text):\n%s\n\nReturn the enrichment JSON as specified.",
		tokenJSON,
	)
	return ub.String()
}

// buildSpanPrompt returns the system and user messages for a span-restricted
// enrichment call (the step-wise per-chunk pass and the incremental "top up"
// pass). The user prompt carries ONLY the tokens inside the requested spans
// (see tokensInSpans) rather than the whole article — this is what keeps each
// per-chunk completion small, so a long article does not blow up the prompt to
// tens of thousands of tokens (and minutes of latency) on every chunk. Token
// indices are the original article indices, so the directive tells the model
// they are not contiguous from zero.
func buildSpanPrompt(a *model.Article, settings model.Settings, enrichmentVersion int, spans []model.Span) (system, user string) {
	ranges := formatSpans(spans)

	system = renderSystemPrompt(settings, enrichmentVersion) +
		"\nSPAN MODE: you are given ONLY a slice of the article's tokens. Their token_index " +
		"values are the original indices from the full article, so they may not start at zero " +
		"or be contiguous. Annotate ONLY the tokens within these inclusive token-index ranges: " +
		ranges + ". Emit difficult_words only when token_index falls inside one of these ranges; " +
		"emit phrases and sentences only when their entire [start_index, end_index] lies inside a " +
		"single range. Do NOT emit anything for tokens outside these ranges. Within the ranges you " +
		"MUST still translate every sentence. Keep using the original token_index values from the input array.\n"
	user = buildUserPrompt(a, tokensInSpans(a.Tokens, spans)) +
		"\n\nAnnotate only these inclusive token-index ranges: " + ranges + "."
	return system, user
}

// tokensInSpans returns the article tokens whose original index falls inside at
// least one of the inclusive spans, in order. This is what lets the step-wise /
// incremental enrichment send only the tokens being annotated: each returned
// token keeps its original index, so the model's token_index references stay
// aligned with the full article. With no spans the full slice is returned.
func tokensInSpans(tokens []model.Token, spans []model.Span) []model.Token {
	if len(spans) == 0 {
		return tokens
	}
	out := make([]model.Token, 0, len(tokens))
	for _, t := range tokens {
		for _, s := range spans {
			if t.Index >= s.Start && t.Index <= s.End {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// formatSpans renders spans as a human/LLM-readable list like "[12-20], [45-45]".
func formatSpans(spans []model.Span) string {
	parts := make([]string, len(spans))
	for i, s := range spans {
		parts[i] = "[" + strconv.Itoa(s.Start) + "-" + strconv.Itoa(s.End) + "]"
	}
	return strings.Join(parts, ", ")
}

// ---------------------------------------------------------------------------
// Retryable error type.
// ---------------------------------------------------------------------------

// APIError is returned on a non-2xx HTTP response. Callers can inspect
// StatusCode to decide whether to retry (429, 5xx).
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("llm: HTTP %d: %s", e.StatusCode, e.Body)
}

// Retryable reports whether the error represents a transient condition that
// the enrichment pool should retry (rate limit or server error).
func (e *APIError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

// DecodeError wraps a failure to decode a successful (2xx) provider response —
// either the chat envelope or the enrichment JSON inside it — together with the
// raw model output that could not be parsed. The enrichment pool persists Raw
// (via the RawResponse() accessor) so the UI can show the unparsed answer for
// inspection.
//
// A DecodeError is retryable only when Transient is set. Transient marks a
// truncated/empty HTTP envelope or an empty completion — the provider returned
// nothing parseable (a network/gateway truncation or a dropped stream), which a
// re-send commonly fixes. A present-but-malformed answer is left non-transient:
// re-sending the same prompt yields the same garbage, so it fails fast and the
// raw output is surfaced for inspection.
type DecodeError struct {
	// Raw is the verbatim text that failed to decode: the message content when
	// the enrichment JSON is malformed, or the whole HTTP body when the chat
	// envelope itself could not be parsed.
	Raw string
	Err error
	// Transient reports whether re-sending the request might succeed (empty /
	// truncated response) rather than reproduce the same failure.
	Transient bool
}

func (e *DecodeError) Error() string { return e.Err.Error() }

func (e *DecodeError) Unwrap() error { return e.Err }

// Retryable reports whether the enrichment pool should retry this decode
// failure. Only transient (empty/truncated) responses are retried.
func (e *DecodeError) Retryable() bool { return e.Transient }

// RawResponse returns the raw model output that failed to decode. It satisfies
// the duck-typed accessor the enrichment pool uses to persist the response
// without importing this package.
func (e *DecodeError) RawResponse() string { return e.Raw }

// ---------------------------------------------------------------------------
// Enrich.
// ---------------------------------------------------------------------------

// Enrich builds the enrichment prompt, calls the OpenAI-compatible API once,
// and returns the parsed model.Enrichment together with provider usage.
// It honours ctx for cancellation and timeout.
func (c *Client) Enrich(ctx context.Context, a *model.Article, settings model.Settings, enrichmentVersion int) (*model.Enrichment, ports.Usage, error) {
	cn, fromProfile := c.resolveConn(ctx)
	systemPrompt, userPrompt := buildPrompt(a, settings, enrichmentVersion)
	return c.complete(ctx, cn, a.ID, effectiveModel(cn, fromProfile, settings.LLMModel), systemPrompt, userPrompt, len(a.Tokens))
}

// EnrichSpans builds an incremental ("top up") prompt restricted to the supplied
// uncovered token spans, calls the API once, and returns the partial enrichment
// (only the annotations for those ranges). The enrich pool merges it into the
// existing enrichment.
func (c *Client) EnrichSpans(ctx context.Context, a *model.Article, settings model.Settings, enrichmentVersion int, spans []model.Span) (*model.Enrichment, ports.Usage, error) {
	cn, fromProfile := c.resolveConn(ctx)
	systemPrompt, userPrompt := buildSpanPrompt(a, settings, enrichmentVersion, spans)
	return c.complete(ctx, cn, a.ID, effectiveModel(cn, fromProfile, settings.LLMModel), systemPrompt, userPrompt, len(a.Tokens))
}

// summarySchema is the JSON Schema for the summary step: a single short
// abstract string. Kept tiny so the completion never truncates.
const summarySchema = `{
  "type": "object",
  "required": ["summary"],
  "additionalProperties": false,
  "properties": {
    "summary": {"type": "string"}
  }
}`

// summaryResponse is the parsed summary completion.
type summaryResponse struct {
	Summary string `json:"summary"`
}

// DefaultSummaryPromptTemplate is the built-in system-prompt template for the
// summary step, used when the user has not configured a custom one via settings.
// The {{target_language}} placeholder is substituted by renderPrompt at request
// time. The JSON schema is enforced separately via response_format.
const DefaultSummaryPromptTemplate = "You are a language-learning assistant. Summarize the following article " +
	"in English in 3–5 concise sentences. Identify the main topic, key arguments, notable facts, and " +
	"recurring domain-specific terms. The summary will be used as background context when translating " +
	"the article into {{target_language}}, so prioritize information that aids comprehension and word-choice. " +
	"Return ONLY the JSON object matching the provided schema. No markdown, no prose."

// buildSummaryPrompt returns the system and user messages for the summary step.
// The system prompt comes from the user's configured summary template, falling
// back to DefaultSummaryPromptTemplate when unset. The output is a short abstract
// in English; the input is the article's plain original text
// (summarization does not need token indices).
func buildSummaryPrompt(a *model.Article, settings model.Settings) (system, user string) {
	template := settings.SummaryPrompt
	if template == "" {
		template = DefaultSummaryPromptTemplate
	}
	system = renderPrompt(template, settings, 0)
	user = "Article title: " + a.Title + "\n\nArticle text:\n" + a.OriginalText
	return system, user
}

// Summarize produces a short abstract of the article in the user's target
// language. It performs exactly one HTTP request; retry/backoff is the caller's
// responsibility (enrich.Pool).
func (c *Client) Summarize(ctx context.Context, a *model.Article, settings model.Settings) (string, ports.Usage, error) {
	cn, fromProfile := c.resolveConn(ctx)
	systemPrompt, userPrompt := buildSummaryPrompt(a, settings)
	reqBody := chatRequest{
		Model: effectiveModel(cn, fromProfile, settings.LLMModel),
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		ResponseFormat: schemaResponseFormat("summary", summarySchema, cn.forceJSONObject),
		Temperature:    0.2,
	}

	content, usage, err := c.postChat(ctx, cn, reqBody)
	if err != nil {
		// When the profile already forces json_object there is no json_schema to
		// fall back from — surface the error directly.
		if !cn.forceJSONObject && isSchemaUnsupported(err) {
			reqBody.ResponseFormat = &responseFormat{Type: "json_object"}
			content, usage, err = c.postChat(ctx, cn, reqBody)
		}
		if err != nil {
			return "", usage, err
		}
	}

	var sr summaryResponse
	if err := json.Unmarshal([]byte(content), &sr); err != nil {
		transient := strings.TrimSpace(content) == ""
		return "", usage, &DecodeError{Raw: content, Err: fmt.Errorf("llm: unmarshal summary content: %w", err), Transient: transient}
	}
	return strings.TrimSpace(sr.Summary), usage, nil
}

// Normalize runs the content-normalization pass of the fetch stage: it sends the
// extracted article text to the LLM with the (possibly user-customized)
// normalization prompt and returns the cleaned body, with the leftover
// navigation / chrome / boilerplate removed. The model replies with the cleaned
// article as plain text / markdown (no JSON wrapper), so the completion is used
// directly. The fail-open guard (normalize.Apply) keeps the original text
// whenever the model returns empty or over-deletes, so a bad pass can never
// destroy the article. It performs exactly one HTTP request; retry/backoff is
// the caller's responsibility (enrich.Pool).
func (c *Client) Normalize(ctx context.Context, title, text string, settings model.Settings) (string, ports.Usage, error) {
	cn, fromProfile := c.resolveConn(ctx)
	systemPrompt := normalize.RenderSystemPrompt(settings)
	userPrompt := "Article title: " + title + "\n\nArticle text:\n" + text
	reqBody := chatRequest{
		Model: effectiveModel(cn, fromProfile, settings.LLMModel),
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0,
	}

	content, usage, err := c.postChat(ctx, cn, reqBody)
	if err != nil {
		return "", usage, err
	}

	cleaned, _ := normalize.Apply(text, content)
	return cleaned, usage, nil
}

// complete issues a single chat completion for the given prompts, decoding the
// enrichment JSON. It prefers the json_schema response format and falls back to
// json_object once for providers that do not support the schema variant (e.g.
// older Ollama). tokenCount is logged for observability only.
func (c *Client) complete(ctx context.Context, cn conn, articleID, modelID, systemPrompt, userPrompt string, tokenCount int) (*model.Enrichment, ports.Usage, error) {
	reqBody := chatRequest{
		Model: modelID,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		ResponseFormat: schemaResponseFormat("enrichment", enrichmentSchema, cn.forceJSONObject),
		Temperature:    0.2,
	}

	slog.Debug("llm: enrich request",
		"article_id", articleID,
		"model", reqBody.Model,
		"token_count", tokenCount,
		"system_prompt_bytes", len(systemPrompt),
		"user_prompt_bytes", len(userPrompt),
		"response_format", reqBody.ResponseFormat.Type,
	)

	enrichment, usage, err := c.do(ctx, cn, reqBody)
	if err != nil {
		// If the provider rejected json_schema, retry once with json_object. When
		// the profile already forces json_object there is nothing to fall back from.
		if !cn.forceJSONObject && isSchemaUnsupported(err) {
			slog.Warn("llm: provider rejected json_schema, falling back to json_object",
				"article_id", articleID,
				"model", reqBody.Model,
				"err", err,
			)
			reqBody.ResponseFormat = &responseFormat{Type: "json_object"}
			enrichment, usage, err = c.do(ctx, cn, reqBody)
		}
	}
	return enrichment, usage, err
}

// do sends the chat request and decodes the enrichment JSON from the first
// choice's message content.
func (c *Client) do(ctx context.Context, cn conn, req chatRequest) (*model.Enrichment, ports.Usage, error) {
	content, usage, err := c.postChat(ctx, cn, req)
	if err != nil {
		return nil, usage, err
	}
	var enrichment model.Enrichment
	if err := json.Unmarshal([]byte(content), &enrichment); err != nil {
		// An empty completion is a truncation/dropped-stream symptom (retry may
		// succeed); a present-but-malformed answer is deterministic (fail fast).
		transient := strings.TrimSpace(content) == ""
		return nil, ports.Usage{}, &DecodeError{Raw: content, Err: fmt.Errorf("llm: unmarshal enrichment content: %w", err), Transient: transient}
	}
	return &enrichment, usage, nil
}

// postChat sends the chat request, validates the HTTP status, and returns the
// first choice's raw message content together with provider usage. Decoding the
// content into a concrete shape is the caller's responsibility.
func (c *Client) postChat(ctx context.Context, cn conn, req chatRequest) (string, ports.Usage, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", ports.Usage{}, fmt.Errorf("llm: marshal request: %w", err)
	}

	url := cn.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", ports.Usage{}, fmt.Errorf("llm: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+cn.apiKey)

	start := time.Now()
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", ports.Usage{}, fmt.Errorf("llm: http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", ports.Usage{}, fmt.Errorf("llm: read response body: %w", err)
	}

	slog.Debug("llm: response received",
		"model", req.Model,
		"status", resp.StatusCode,
		"body_bytes", len(respBody),
		"duration_ms", time.Since(start).Milliseconds(),
	)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := string(respBody)
		if len(snippet) > 256 {
			snippet = snippet[:256] + "..."
		}
		return "", ports.Usage{}, &APIError{StatusCode: resp.StatusCode, Body: snippet}
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		// The envelope is provider-generated JSON; a parse failure means the body
		// was empty or truncated in transit (e.g. "unexpected end of JSON input"),
		// which a re-send commonly fixes — so treat it as transient/retryable.
		return "", ports.Usage{}, &DecodeError{Raw: string(respBody), Err: fmt.Errorf("llm: unmarshal response: %w", err), Transient: true}
	}

	if len(chatResp.Choices) == 0 {
		return "", ports.Usage{}, &DecodeError{Raw: string(respBody), Err: fmt.Errorf("llm: response contains no choices"), Transient: true}
	}

	content := chatResp.Choices[0].Message.Content

	usage := ports.Usage{}
	if chatResp.Usage != nil {
		usage = ports.Usage{
			PromptTokens:     chatResp.Usage.PromptTokens,
			CompletionTokens: chatResp.Usage.CompletionTokens,
			TotalTokens:      chatResp.Usage.TotalTokens,
		}
		slog.Info("llm usage",
			"model", req.Model,
			"prompt_tokens", usage.PromptTokens,
			"completion_tokens", usage.CompletionTokens,
			"total_tokens", usage.TotalTokens,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}

	return content, usage, nil
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

// modelName picks the effective model: the per-user settings override wins
// when non-empty; otherwise the deployment-level default is used.
func modelName(defaultModel, settingsModel string) string {
	if settingsModel != "" {
		return settingsModel
	}
	return defaultModel
}

// isSchemaUnsupported heuristically detects whether the provider rejected the
// request because it cannot serve the json_schema response format, so we can
// retry with the simpler json_object format.
//
// Two distinct provider behaviours are covered:
//
//   - 400 Bad Request: the provider does not recognise json_schema but supports
//     json_object (e.g. older Ollama).
//   - 404 Not Found: OpenRouter returns "No endpoints available ..." when the
//     json_schema response format forces a structured-outputs provider filter
//     that, combined with the active guardrail/data-policy, leaves zero eligible
//     endpoints. The same request without json_schema routes successfully.
func isSchemaUnsupported(err error) bool {
	if err == nil {
		return false
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		return false
	}
	lower := strings.ToLower(apiErr.Body)
	switch apiErr.StatusCode {
	case http.StatusBadRequest:
		return strings.Contains(lower, "json_schema") ||
			strings.Contains(lower, "response_format") ||
			strings.Contains(lower, "unsupported")
	case http.StatusNotFound:
		return strings.Contains(lower, "no endpoints")
	default:
		return false
	}
}
