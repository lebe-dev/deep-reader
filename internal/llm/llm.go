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
	"deep-reader/internal/ports"
)

// Client is the LLM client. Construct it with New(cfg).
type Client struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// New creates a new Client from cfg. It satisfies ports.LLMClient.
func New(cfg *config.Config) *Client {
	baseURL := strings.TrimRight(cfg.LLMAPIBaseURL, "/")
	return &Client{
		baseURL: baseURL,
		apiKey:  cfg.LLMAPIKey,
		model:   cfg.LLMModel,
		httpClient: &http.Client{
			Timeout: cfg.LLMRequestTimeout,
		},
	}
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

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	ResponseFormat responseFormat `json:"response_format"`
	Temperature    float64        `json:"temperature"`
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
	template := settings.EnrichmentPrompt
	if template == "" {
		template = DefaultEnrichmentPromptTemplate
	}
	system = renderPrompt(template, settings, enrichmentVersion)

	// User prompt: indexed token array.
	type indexedToken struct {
		Index int    `json:"index"`
		Text  string `json:"text"`
	}
	tokens := make([]indexedToken, len(a.Tokens))
	for i, t := range a.Tokens {
		tokens[i] = indexedToken{Index: t.Index, Text: t.Text}
	}
	tokenJSON, _ := json.Marshal(tokens)

	var ub strings.Builder
	fmt.Fprintf(&ub,
		"Article title: %s\n\nTokenized text (index → text):\n%s\n\n"+
			"Return the enrichment JSON as specified.",
		a.Title, tokenJSON,
	)
	user = ub.String()
	return system, user
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

// ---------------------------------------------------------------------------
// Enrich.
// ---------------------------------------------------------------------------

// Enrich builds the enrichment prompt, calls the OpenAI-compatible API once,
// and returns the parsed model.Enrichment together with provider usage.
// It honours ctx for cancellation and timeout.
func (c *Client) Enrich(ctx context.Context, a *model.Article, settings model.Settings, enrichmentVersion int) (*model.Enrichment, ports.Usage, error) {
	systemPrompt, userPrompt := buildPrompt(a, settings, enrichmentVersion)

	// Prefer json_schema response format; fall back to json_object for
	// providers that do not support the schema variant (e.g. older Ollama).
	reqBody := chatRequest{
		Model: modelName(c.model, settings.LLMModel),
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		ResponseFormat: responseFormat{
			Type: "json_schema",
			JSONSchema: &jsonSchema{
				Name:   "enrichment",
				Schema: json.RawMessage(enrichmentSchema),
				Strict: true,
			},
		},
		Temperature: 0.2,
	}

	slog.Debug("llm: enrich request",
		"article_id", a.ID,
		"model", reqBody.Model,
		"token_count", len(a.Tokens),
		"system_prompt_bytes", len(systemPrompt),
		"user_prompt_bytes", len(userPrompt),
		"response_format", reqBody.ResponseFormat.Type,
	)

	enrichment, usage, err := c.do(ctx, reqBody)
	if err != nil {
		// If the provider rejected json_schema, retry once with json_object.
		if isSchemaUnsupported(err) {
			slog.Warn("llm: provider rejected json_schema, falling back to json_object",
				"article_id", a.ID,
				"model", reqBody.Model,
				"err", err,
			)
			reqBody.ResponseFormat = responseFormat{Type: "json_object"}
			reqBody.ResponseFormat.JSONSchema = nil
			enrichment, usage, err = c.do(ctx, reqBody)
		}
	}
	return enrichment, usage, err
}

// do sends the chat request and decodes the enrichment JSON from the first
// choice's message content.
func (c *Client) do(ctx context.Context, req chatRequest) (*model.Enrichment, ports.Usage, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, ports.Usage{}, fmt.Errorf("llm: marshal request: %w", err)
	}

	url := c.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, ports.Usage{}, fmt.Errorf("llm: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	start := time.Now()
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, ports.Usage{}, fmt.Errorf("llm: http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, ports.Usage{}, fmt.Errorf("llm: read response body: %w", err)
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
		return nil, ports.Usage{}, &APIError{StatusCode: resp.StatusCode, Body: snippet}
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, ports.Usage{}, fmt.Errorf("llm: unmarshal response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, ports.Usage{}, fmt.Errorf("llm: response contains no choices")
	}

	content := chatResp.Choices[0].Message.Content
	var enrichment model.Enrichment
	if err := json.Unmarshal([]byte(content), &enrichment); err != nil {
		return nil, ports.Usage{}, fmt.Errorf("llm: unmarshal enrichment content: %w", err)
	}

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

	return &enrichment, usage, nil
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
