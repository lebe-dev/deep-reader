package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"deep-reader/internal/config"
	"deep-reader/internal/llm"
	"deep-reader/internal/model"
	"deep-reader/internal/ports"
)

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

// testConfig returns a minimal Config wired to the given server URL.
func testConfig(serverURL string) *config.Config {
	return &config.Config{
		LLMAPIBaseURL:     serverURL,
		LLMAPIKey:         "test-key",
		LLMModel:          "test-model",
		LLMRequestTimeout: 10 * time.Second,
	}
}

// testArticle returns a minimal article with a couple of tokens.
func testArticle() *model.Article {
	return &model.Article{
		ID:    "01HZZ000000000000000000001",
		Title: "Test Article",
		Tokens: []model.Token{
			{Index: 0, Text: "The", Start: 0, End: 3},
			{Index: 1, Text: "quick", Start: 4, End: 9},
			{Index: 2, Text: "brown", Start: 10, End: 15},
			{Index: 3, Text: "fox", Start: 16, End: 19},
		},
		OriginalText: "The quick brown fox",
	}
}

// testSettings returns minimal settings.
func testSettings() model.Settings {
	return model.Settings{
		CEFRLevel:                model.CEFRB1,
		TargetLanguage:           model.DefaultTargetLanguage,
		LLMModel:                 "",
		MinDifficultyToHighlight: model.CEFRB2,
	}
}

// cannedEnrichment is the JSON the fake server returns as the choice content.
const cannedContent = `{
  "difficult_words": [
    {"token_index": 1, "lemma": "quick", "translation": "быстрый", "cefr_level": "B2"}
  ],
  "phrases": [
    {"start_index": 1, "end_index": 3, "type": "idiom", "translation": "быстрая коричневая лиса"}
  ],
  "sentences": [
    {"start_index": 0, "end_index": 3, "translation": "Быстрая коричневая лиса"}
  ],
  "glossary": [
    {"term": "fox", "definition": "лиса — хитрый зверь"}
  ]
}`

// buildCannedResponse wraps cannedContent in a minimal OpenAI-compatible
// chat completion response with usage.
func buildCannedResponse(t *testing.T) []byte {
	t.Helper()
	type usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type choice struct {
		Message message `json:"message"`
	}
	type resp struct {
		Choices []choice `json:"choices"`
		Usage   usage    `json:"usage"`
	}
	r := resp{
		Choices: []choice{{Message: message{Role: "assistant", Content: cannedContent}}},
		Usage:   usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150},
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("buildCannedResponse: %v", err)
	}
	return b
}

// ---------------------------------------------------------------------------
// Tests.
// ---------------------------------------------------------------------------

// TestEnrich_RequestShape asserts that Enrich sends a POST to
// /chat/completions with the correct Authorization header, the configured
// model, and a non-empty messages array. The server returns a canned
// enrichment payload.
func TestEnrich_RequestShape(t *testing.T) {
	var capturedReq struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		ResponseFormat struct {
			Type string `json:"type"`
		} `json:"response_format"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Assert path.
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// Assert method.
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		// Assert Authorization header.
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-key" {
			t.Errorf("unexpected Authorization header: %q", auth)
		}
		// Decode request body.
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &capturedReq); err != nil {
			t.Fatalf("unmarshal request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buildCannedResponse(t))
	}))
	defer srv.Close()

	client := llm.New(testConfig(srv.URL))
	_, _, err := client.Enrich(context.Background(), testArticle(), testSettings(), 1)
	if err != nil {
		t.Fatalf("Enrich returned error: %v", err)
	}

	// Assert model.
	if capturedReq.Model != "test-model" {
		t.Errorf("model = %q, want %q", capturedReq.Model, "test-model")
	}
	// Assert messages: must have system + user.
	if len(capturedReq.Messages) < 2 {
		t.Errorf("expected at least 2 messages, got %d", len(capturedReq.Messages))
	}
	if capturedReq.Messages[0].Role != "system" {
		t.Errorf("first message role = %q, want system", capturedReq.Messages[0].Role)
	}
	if capturedReq.Messages[1].Role != "user" {
		t.Errorf("second message role = %q, want user", capturedReq.Messages[1].Role)
	}
	// Assert response_format is set (json_schema or json_object).
	if capturedReq.ResponseFormat.Type == "" {
		t.Error("response_format.type must not be empty")
	}
}

// TestEnrich_DecodesEnrichment asserts that the canned JSON response decodes
// correctly into model.Enrichment.
func TestEnrich_DecodesEnrichment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buildCannedResponse(t))
	}))
	defer srv.Close()

	client := llm.New(testConfig(srv.URL))
	enrichment, usage, err := client.Enrich(context.Background(), testArticle(), testSettings(), 1)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}

	// difficult_words.
	if len(enrichment.DifficultWords) != 1 {
		t.Fatalf("DifficultWords len = %d, want 1", len(enrichment.DifficultWords))
	}
	dw := enrichment.DifficultWords[0]
	if dw.TokenIndex != 1 {
		t.Errorf("DifficultWords[0].TokenIndex = %d, want 1", dw.TokenIndex)
	}
	if dw.Lemma != "quick" {
		t.Errorf("DifficultWords[0].Lemma = %q, want quick", dw.Lemma)
	}
	if dw.Translation != "быстрый" {
		t.Errorf("DifficultWords[0].Translation = %q, want быстрый", dw.Translation)
	}
	if dw.CEFRLevel != "B2" {
		t.Errorf("DifficultWords[0].CEFRLevel = %q, want B2", dw.CEFRLevel)
	}

	// phrases.
	if len(enrichment.Phrases) != 1 {
		t.Fatalf("Phrases len = %d, want 1", len(enrichment.Phrases))
	}
	ph := enrichment.Phrases[0]
	if ph.StartIndex != 1 || ph.EndIndex != 3 {
		t.Errorf("Phrases[0] range = [%d,%d], want [1,3]", ph.StartIndex, ph.EndIndex)
	}
	if ph.Type != model.PhraseTypeIdiom {
		t.Errorf("Phrases[0].Type = %q, want idiom", ph.Type)
	}

	// sentences.
	if len(enrichment.Sentences) != 1 {
		t.Fatalf("Sentences len = %d, want 1", len(enrichment.Sentences))
	}
	s := enrichment.Sentences[0]
	if s.StartIndex != 0 || s.EndIndex != 3 {
		t.Errorf("Sentences[0] range = [%d,%d], want [0,3]", s.StartIndex, s.EndIndex)
	}

	// glossary.
	if len(enrichment.Glossary) != 1 {
		t.Fatalf("Glossary len = %d, want 1", len(enrichment.Glossary))
	}
	g := enrichment.Glossary[0]
	if g.Term != "fox" {
		t.Errorf("Glossary[0].Term = %q, want fox", g.Term)
	}

	// usage.
	wantUsage := ports.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150}
	if usage != wantUsage {
		t.Errorf("usage = %+v, want %+v", usage, wantUsage)
	}
}

// TestEnrich_AuthHeader asserts that every request carries the Authorization
// header with the correct Bearer token.
func TestEnrich_AuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buildCannedResponse(t))
	}))
	defer srv.Close()

	client := llm.New(testConfig(srv.URL))
	_, _, _ = client.Enrich(context.Background(), testArticle(), testSettings(), 1)

	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-key")
	}
}

// TestEnrich_Non2xxReturnsAPIError asserts that a non-2xx status results in an
// error, and that 429/5xx are flagged as retryable.
func TestEnrich_Non2xxReturnsAPIError(t *testing.T) {
	cases := []struct {
		statusCode int
		retryable  bool
	}{
		{http.StatusUnauthorized, false},
		{http.StatusTooManyRequests, true},
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
	}

	for _, tc := range cases {
		t.Run(http.StatusText(tc.statusCode), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "error body", tc.statusCode)
			}))
			defer srv.Close()

			client := llm.New(testConfig(srv.URL))
			_, _, err := client.Enrich(context.Background(), testArticle(), testSettings(), 1)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			apiErr, ok := err.(*llm.APIError)
			if !ok {
				t.Fatalf("expected *llm.APIError, got %T: %v", err, err)
			}
			if apiErr.StatusCode != tc.statusCode {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tc.statusCode)
			}
			if apiErr.Retryable() != tc.retryable {
				t.Errorf("Retryable() = %v, want %v", apiErr.Retryable(), tc.retryable)
			}
		})
	}
}

// TestEnrich_DecodeErrorCarriesRaw asserts that when a 2xx response carries
// malformed enrichment content, Enrich returns a *llm.DecodeError whose
// RawResponse() is the verbatim (undecodable) model output and that is not
// flagged retryable.
func TestEnrich_DecodeErrorCarriesRaw(t *testing.T) {
	const badContent = `{"sentences": [ {"start_index": 0, "end_` // truncated JSON
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		type choice struct {
			Message message `json:"message"`
		}
		type resp struct {
			Choices []choice `json:"choices"`
		}
		b, _ := json.Marshal(resp{Choices: []choice{{Message: message{Role: "assistant", Content: badContent}}}})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	client := llm.New(testConfig(srv.URL))
	_, _, err := client.Enrich(context.Background(), testArticle(), testSettings(), 1)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var decErr *llm.DecodeError
	if !errors.As(err, &decErr) {
		t.Fatalf("expected *llm.DecodeError, got %T: %v", err, err)
	}
	if decErr.RawResponse() != badContent {
		t.Errorf("RawResponse() = %q, want %q", decErr.RawResponse(), badContent)
	}

	// A present-but-malformed answer is permanent: retrying yields the same bad
	// answer, so Retryable() must be false (the content was non-empty garbage).
	if decErr.Retryable() {
		t.Error("present-but-malformed DecodeError should not be retryable")
	}
}

// TestEnrich_DecodeErrorCarriesBodyOnEnvelope asserts that when the chat
// envelope itself is unparseable, the raw HTTP body is captured.
func TestEnrich_DecodeErrorCarriesBodyOnEnvelope(t *testing.T) {
	const badBody = `not json at all`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(badBody))
	}))
	defer srv.Close()

	client := llm.New(testConfig(srv.URL))
	_, _, err := client.Enrich(context.Background(), testArticle(), testSettings(), 1)
	var decErr *llm.DecodeError
	if !errors.As(err, &decErr) {
		t.Fatalf("expected *llm.DecodeError, got %T: %v", err, err)
	}
	if decErr.RawResponse() != badBody {
		t.Errorf("RawResponse() = %q, want %q", decErr.RawResponse(), badBody)
	}
}

// TestEnrich_ContextCancellation asserts that a cancelled context results in
// an error (not a panic or silent ignore).
func TestEnrich_ContextCancellation(t *testing.T) {
	// Server that blocks until the test ends — the client should cancel first.
	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-unblock
	}))
	defer func() {
		close(unblock)
		srv.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	client := llm.New(testConfig(srv.URL))
	_, _, err := client.Enrich(ctx, testArticle(), testSettings(), 1)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

// TestEnrich_SettingsModelOverride asserts that when Settings.LLMModel is set
// it is used in the request instead of the config-level default.
func TestEnrich_SettingsModelOverride(t *testing.T) {
	var capturedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		capturedModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buildCannedResponse(t))
	}))
	defer srv.Close()

	settings := testSettings()
	settings.LLMModel = "gpt-4o"

	client := llm.New(testConfig(srv.URL))
	_, _, err := client.Enrich(context.Background(), testArticle(), settings, 1)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if capturedModel != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", capturedModel)
	}
}

// fakeResolver is a ports.LLMProviderResolver returning a fixed active profile,
// used to exercise the provider-profile model-precedence path.
type fakeResolver struct {
	provider model.LLMProvider
}

func (f fakeResolver) GetActiveLLMProvider(context.Context) (model.LLMProvider, error) {
	return f.provider, nil
}

// noProviderResolver is a ports.LLMProviderResolver with no active profile,
// modelling a deployment where every UI profile has been deleted.
type noProviderResolver struct{}

func (noProviderResolver) GetActiveLLMProvider(context.Context) (model.LLMProvider, error) {
	return model.LLMProvider{}, ports.ErrNotFound
}

// TestEnrich_NoEnvFallbackWhenResolverPresent asserts that once a resolver is
// wired (the production path), the env-derived cfg connection is never used at
// request time: with no active profile the call must fail without ever sending
// the configured env model/key to the cfg base URL. This guards the rule that
// the backend reads the model and token only from the UI, never from env.
func TestEnrich_NoEnvFallbackWhenResolverPresent(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buildCannedResponse(t))
	}))
	defer srv.Close()

	// testConfig wires the env-derived base URL/key/model to the fake server.
	// With a resolver present but no active profile, none of it must be used.
	client := llm.New(testConfig(srv.URL), llm.WithProviderResolver(noProviderResolver{}))
	if _, _, err := client.Enrich(context.Background(), testArticle(), testSettings(), 1); err == nil {
		t.Fatal("expected an error when no active profile is configured, got nil")
	}
	if hits != 0 {
		t.Errorf("env-configured server was hit %d time(s); the cfg/env connection must not be used when a resolver is wired", hits)
	}
}

// captureModelWithProvider runs Enrich against a fake server while an active
// provider profile (with the given model) is resolved, returning the model name
// the client put in the request body. The resolved profile's base URL is wired
// to the fake server so the request still lands there.
func captureModelWithProvider(t *testing.T, settings model.Settings, providerModel string) string {
	t.Helper()
	var capturedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		capturedModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buildCannedResponse(t))
	}))
	defer srv.Close()

	resolver := fakeResolver{provider: model.LLMProvider{BaseURL: srv.URL, Model: providerModel}}
	client := llm.New(testConfig(srv.URL), llm.WithProviderResolver(resolver))
	if _, _, err := client.Enrich(context.Background(), testArticle(), settings, 1); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	return capturedModel
}

// TestEnrich_ActiveProviderModelWinsOverSettings asserts that when an active
// provider profile supplies a model, it is used for the request even if the
// legacy settings.llm_model carries a different (stale) value. This is the
// regression guard for the bug where a stale settings.llm_model overrode the
// active profile's model.
func TestEnrich_ActiveProviderModelWinsOverSettings(t *testing.T) {
	settings := testSettings()
	settings.LLMModel = "deepseek/deepseek-v4-flash" // stale per-user value

	got := captureModelWithProvider(t, settings, "deepseek/deepseek-v4-pro")
	if got != "deepseek/deepseek-v4-pro" {
		t.Errorf("model = %q, want active profile's deepseek/deepseek-v4-pro", got)
	}
}

// TestEnrich_SettingsModelFillsBlankProfileModel asserts that when the active
// profile leaves its model blank (e.g. an env-seeded "Default" profile), the
// legacy settings.llm_model fills in.
func TestEnrich_SettingsModelFillsBlankProfileModel(t *testing.T) {
	settings := testSettings()
	settings.LLMModel = "gpt-4o"

	got := captureModelWithProvider(t, settings, "") // blank — profile defers to settings
	if got != "gpt-4o" {
		t.Errorf("model = %q, want settings fallback gpt-4o", got)
	}
}

// TestEnrich_PromptContainsTokens asserts that the user message references the
// article tokens and that the system prompt contains the CEFR level and target
// language.
func TestEnrich_PromptContainsTokens(t *testing.T) {
	var systemPrompt, userPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		for _, m := range body.Messages {
			switch m.Role {
			case "system":
				systemPrompt = m.Content
			case "user":
				userPrompt = m.Content
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buildCannedResponse(t))
	}))
	defer srv.Close()

	client := llm.New(testConfig(srv.URL))
	settings := testSettings()
	_, _, err := client.Enrich(context.Background(), testArticle(), settings, 1)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}

	// System prompt must mention the CEFR level and target language.
	if !strings.Contains(systemPrompt, settings.CEFRLevel) {
		t.Errorf("system prompt does not contain CEFR level %q", settings.CEFRLevel)
	}
	if !strings.Contains(systemPrompt, settings.TargetLanguage) {
		t.Errorf("system prompt does not contain target language %q", settings.TargetLanguage)
	}
	if !strings.Contains(systemPrompt, settings.MinDifficultyToHighlight) {
		t.Errorf("system prompt does not contain min difficulty %q", settings.MinDifficultyToHighlight)
	}

	// User prompt must contain at least one token text from the article.
	if !strings.Contains(userPrompt, "quick") {
		t.Error("user prompt does not contain article token text 'quick'")
	}
}

// summaryServer returns a fake summary completion and captures the system
// prompt the client sent.
func summaryServer(t *testing.T, capture *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		for _, m := range body.Messages {
			if m.Role == "system" {
				*capture = m.Content
			}
		}
		resp := `{"choices":[{"message":{"content":"{\"summary\":\"краткое содержание\"}"}}],` +
			`"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(resp))
	}))
}

// TestSummarize_DefaultPrompt asserts the summary step uses the built-in default
// template (with {{target_language}} substituted) and decodes the summary.
func TestSummarize_DefaultPrompt(t *testing.T) {
	var systemPrompt string
	srv := summaryServer(t, &systemPrompt)
	defer srv.Close()

	client := llm.New(testConfig(srv.URL))
	settings := testSettings()
	summary, _, err := client.Summarize(context.Background(), testArticle(), settings)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if summary != "краткое содержание" {
		t.Errorf("unexpected summary %q", summary)
	}
	// The {{target_language}} placeholder must have been substituted.
	if !strings.Contains(systemPrompt, settings.TargetLanguage) {
		t.Errorf("system prompt does not contain target language %q: %q", settings.TargetLanguage, systemPrompt)
	}
	if strings.Contains(systemPrompt, "{{target_language}}") {
		t.Error("system prompt still contains the unsubstituted {{target_language}} placeholder")
	}
}

// TestSummarize_CustomPrompt asserts a user-configured summary template overrides
// the built-in default.
func TestSummarize_CustomPrompt(t *testing.T) {
	var systemPrompt string
	srv := summaryServer(t, &systemPrompt)
	defer srv.Close()

	client := llm.New(testConfig(srv.URL))
	settings := testSettings()
	settings.SummaryPrompt = "CUSTOM SUMMARY in {{target_language}} please."
	if _, _, err := client.Summarize(context.Background(), testArticle(), settings); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if !strings.Contains(systemPrompt, "CUSTOM SUMMARY") {
		t.Errorf("custom summary prompt was not used: %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, settings.TargetLanguage) {
		t.Errorf("custom prompt placeholder not substituted: %q", systemPrompt)
	}
}

// TestEnrich_FallbackToJSONObject asserts that when the provider returns a 400
// that looks like "json_schema not supported", the client retries with
// json_object and succeeds.
func TestEnrich_FallbackToJSONObject(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var body struct {
			ResponseFormat struct {
				Type string `json:"type"`
			} `json:"response_format"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)

		if body.ResponseFormat.Type == "json_schema" {
			// Simulate a provider that does not support json_schema.
			http.Error(w, `{"error":"json_schema is unsupported by this provider"}`, http.StatusBadRequest)
			return
		}
		// Second call with json_object succeeds.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buildCannedResponse(t))
	}))
	defer srv.Close()

	client := llm.New(testConfig(srv.URL))
	enrichment, _, err := client.Enrich(context.Background(), testArticle(), testSettings(), 1)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls (json_schema then json_object), got %d", callCount)
	}
	if len(enrichment.DifficultWords) == 0 {
		t.Error("expected enrichment.DifficultWords to be populated on fallback")
	}
}

// TestEnrich_FallbackOn404NoEndpoints asserts that when the provider returns a
// 404 "No endpoints available" (OpenRouter's response when json_schema forces a
// structured-outputs filter that no allowed provider satisfies), the client
// retries with json_object and succeeds.
func TestEnrich_FallbackOn404NoEndpoints(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var body struct {
			ResponseFormat struct {
				Type string `json:"type"`
			} `json:"response_format"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)

		if body.ResponseFormat.Type == "json_schema" {
			// Mirror OpenRouter's 404 when no endpoint matches the
			// structured-outputs requirement under the active guardrail.
			http.Error(w, `{"error":{"message":"No endpoints available matching your guardrail restrictions and data policy. Configure: https://openrouter.ai/settings/privacy","code":404}}`, http.StatusNotFound)
			return
		}
		// Second call with json_object succeeds.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buildCannedResponse(t))
	}))
	defer srv.Close()

	client := llm.New(testConfig(srv.URL))
	enrichment, _, err := client.Enrich(context.Background(), testArticle(), testSettings(), 1)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls (json_schema then json_object), got %d", callCount)
	}
	if len(enrichment.DifficultWords) == 0 {
		t.Error("expected enrichment.DifficultWords to be populated on fallback")
	}
}

// TestEnrich_UsageZeroWhenAbsent asserts that a response without a usage field
// results in a zero-value ports.Usage (not an error).
func TestEnrich_UsageZeroWhenAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Response without usage field — content must be a JSON-encoded string.
		type message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		type choice struct {
			Message message `json:"message"`
		}
		type resp struct {
			Choices []choice `json:"choices"`
		}
		r2 := resp{Choices: []choice{{Message: message{Role: "assistant", Content: cannedContent}}}}
		b, _ := json.Marshal(r2)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	client := llm.New(testConfig(srv.URL))
	_, usage, err := client.Enrich(context.Background(), testArticle(), testSettings(), 1)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	want := ports.Usage{}
	if usage != want {
		t.Errorf("usage = %+v, want zero value", usage)
	}
}

// TestEnrich_EnrichmentVersionInPrompt asserts that different enrichment
// version values produce different system prompts (i.e. the version is
// captured in the prompt).
func TestEnrich_EnrichmentVersionInPrompt(t *testing.T) {
	var capturedPrompts [2]string
	call := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		for _, m := range body.Messages {
			if m.Role == "system" {
				capturedPrompts[call] = m.Content
			}
		}
		call++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buildCannedResponse(t))
	}))
	defer srv.Close()

	client := llm.New(testConfig(srv.URL))
	_, _, _ = client.Enrich(context.Background(), testArticle(), testSettings(), 1)
	_, _, _ = client.Enrich(context.Background(), testArticle(), testSettings(), 2)

	if capturedPrompts[0] == capturedPrompts[1] {
		t.Error("system prompts for version 1 and version 2 are identical; expected them to differ")
	}
}

// captureSystemPrompt runs Enrich against a fake server and returns the system
// message the client sent.
func captureSystemPrompt(t *testing.T, settings model.Settings, version int) string {
	t.Helper()
	var systemPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		for _, m := range body.Messages {
			if m.Role == "system" {
				systemPrompt = m.Content
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buildCannedResponse(t))
	}))
	defer srv.Close()

	client := llm.New(testConfig(srv.URL))
	if _, _, err := client.Enrich(context.Background(), testArticle(), settings, version); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	return systemPrompt
}

// TestEnrich_UsesDefaultPromptWhenUnset asserts that an empty
// settings.EnrichmentPrompt falls back to the built-in default template (and
// that its placeholders are substituted, not sent literally).
func TestEnrich_UsesDefaultPromptWhenUnset(t *testing.T) {
	settings := testSettings()
	settings.EnrichmentPrompt = ""

	got := captureSystemPrompt(t, settings, 2)

	if !strings.Contains(got, "language-learning assistant") {
		t.Errorf("default prompt not used; got %q", got)
	}
	if strings.Contains(got, "{{target_language}}") || strings.Contains(got, "{{cefr_level}}") {
		t.Errorf("placeholders were not substituted in default prompt: %q", got)
	}
	if !strings.Contains(got, settings.CEFRLevel) {
		t.Errorf("default prompt missing CEFR level %q", settings.CEFRLevel)
	}
}

// TestEnrich_UsesCustomPromptWithPlaceholders asserts that a configured
// enrichment prompt replaces the default and that its placeholders are
// substituted from settings and the enrichment version.
func TestEnrich_UsesCustomPromptWithPlaceholders(t *testing.T) {
	settings := testSettings()
	settings.EnrichmentPrompt = "CUSTOM v{{enrichment_version}} lang={{target_language}} level={{cefr_level}} min={{min_difficulty}}"

	got := captureSystemPrompt(t, settings, 7)

	want := "CUSTOM v7 lang=" + settings.TargetLanguage +
		" level=" + settings.CEFRLevel + " min=" + settings.MinDifficultyToHighlight
	if got != want {
		t.Errorf("custom prompt mismatch:\n got %q\nwant %q", got, want)
	}
	if strings.Contains(got, "language-learning assistant") {
		t.Error("default prompt leaked into custom prompt output")
	}
}

// TestEnrichSpans_RestrictsToRanges verifies the incremental prompt carries the
// requested span ranges and that the response is decoded like a normal enrich.
func TestEnrichSpans_RestrictsToRanges(t *testing.T) {
	var systemPrompt, userPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		for _, m := range body.Messages {
			switch m.Role {
			case "system":
				systemPrompt = m.Content
			case "user":
				userPrompt = m.Content
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buildCannedResponse(t))
	}))
	defer srv.Close()

	client := llm.New(testConfig(srv.URL))
	spans := []model.Span{{Start: 1, End: 3}}
	enr, _, err := client.EnrichSpans(context.Background(), testArticle(), testSettings(), 1, spans)
	if err != nil {
		t.Fatalf("EnrichSpans: %v", err)
	}
	if enr == nil || len(enr.Sentences) != 1 {
		t.Fatalf("expected decoded enrichment with 1 sentence, got %+v", enr)
	}

	// Both prompts must reference the requested range so the model restricts itself.
	if !strings.Contains(systemPrompt, "SPAN MODE") {
		t.Error("system prompt missing span-mode directive")
	}
	if !strings.Contains(systemPrompt, "[1-3]") {
		t.Errorf("system prompt missing span range [1-3]: %q", systemPrompt)
	}
	if !strings.Contains(userPrompt, "[1-3]") {
		t.Errorf("user prompt missing span range [1-3]: %q", userPrompt)
	}

	// The user prompt must carry ONLY the spanned tokens (indices 1–3), not the
	// whole article — this is the change that keeps per-chunk prompts small.
	if !strings.Contains(userPrompt, `"index":1`) ||
		!strings.Contains(userPrompt, `"index":2`) ||
		!strings.Contains(userPrompt, `"index":3`) {
		t.Errorf("user prompt missing spanned tokens 1-3: %q", userPrompt)
	}
	if strings.Contains(userPrompt, `"index":0`) {
		t.Errorf("user prompt leaked out-of-span token 0 (full article sent?): %q", userPrompt)
	}
}

// TestEnrich_EmptyBodyIsRetryable asserts that a 2xx response with an empty
// body (a truncated / dropped stream — the "unexpected end of JSON input" case)
// yields a retryable DecodeError so the enrichment pool re-sends instead of
// failing the chunk permanently.
func TestEnrich_EmptyBodyIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Write nothing: an empty body.
	}))
	defer srv.Close()

	client := llm.New(testConfig(srv.URL))
	_, _, err := client.Enrich(context.Background(), testArticle(), testSettings(), 1)
	var decErr *llm.DecodeError
	if !errors.As(err, &decErr) {
		t.Fatalf("expected *llm.DecodeError, got %T: %v", err, err)
	}
	if !decErr.Retryable() {
		t.Error("empty/truncated envelope DecodeError should be retryable")
	}
}

// TestEnrich_EmptyCompletionIsRetryable asserts that a valid envelope carrying
// an empty completion content is retryable (the model returned nothing).
func TestEnrich_EmptyCompletionIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		type choice struct {
			Message message `json:"message"`
		}
		type resp struct {
			Choices []choice `json:"choices"`
		}
		b, _ := json.Marshal(resp{Choices: []choice{{Message: message{Role: "assistant", Content: "  "}}}})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	client := llm.New(testConfig(srv.URL))
	_, _, err := client.Enrich(context.Background(), testArticle(), testSettings(), 1)
	var decErr *llm.DecodeError
	if !errors.As(err, &decErr) {
		t.Fatalf("expected *llm.DecodeError, got %T: %v", err, err)
	}
	if !decErr.Retryable() {
		t.Error("empty-completion DecodeError should be retryable")
	}
}
