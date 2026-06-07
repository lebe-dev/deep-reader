package api

import (
	"net/http"
	"testing"

	"deep-reader/internal/model"
)

func TestLLMProviders_ListMasksKey(t *testing.T) {
	st := &fakeStore{providers: []model.LLMProvider{
		{ID: "p1", Name: "OpenRouter", BaseURL: "https://api.example.com/v1", APIKey: "sk-secret-1234", Model: "gpt-4o-mini", IsActive: true},
	}}
	s := newTestServer(t, st, &fakeIngestor{})

	resp := doReq(t, s, http.MethodGet, "/api/llm-providers", nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", resp.StatusCode)
	}
	body := decode[struct {
		Providers []model.LLMProviderView `json:"providers"`
	}](t, resp)
	if len(body.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(body.Providers))
	}
	p := body.Providers[0]
	if p.KeyPreview != "••••1234" {
		t.Errorf("key preview = %q, want masked", p.KeyPreview)
	}
	if !p.HasKey {
		t.Errorf("has_key should be true")
	}
}

func TestLLMProviders_CreateValidates(t *testing.T) {
	st := &fakeStore{}
	s := newTestServer(t, st, &fakeIngestor{})

	// Missing base_url => 400.
	resp := doReq(t, s, http.MethodPost, "/api/llm-providers", model.LLMProviderInput{Name: "A", Model: "m"}, testToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create without base_url status = %d, want 400", resp.StatusCode)
	}

	// Non-http base_url => 400.
	resp = doReq(t, s, http.MethodPost, "/api/llm-providers", model.LLMProviderInput{Name: "A", BaseURL: "ftp://x", Model: "m"}, testToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create with bad scheme status = %d, want 400", resp.StatusCode)
	}

	// Valid => 201, first becomes active.
	key := "sk-abc"
	resp = doReq(t, s, http.MethodPost, "/api/llm-providers",
		model.LLMProviderInput{Name: "A", BaseURL: "https://a/v1", Model: "m", APIKey: &key}, testToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	view := decode[model.LLMProviderView](t, resp)
	if !view.IsActive {
		t.Errorf("first provider should be active")
	}
	if view.KeyPreview == "sk-abc" {
		t.Errorf("response must not leak the raw key")
	}
}

func TestLLMProviders_ActivateAndDeleteUnknown(t *testing.T) {
	st := &fakeStore{providers: []model.LLMProvider{
		{ID: "p1", Name: "A", BaseURL: "https://a", Model: "m", IsActive: true},
		{ID: "p2", Name: "B", BaseURL: "https://b", Model: "m"},
	}}
	s := newTestServer(t, st, &fakeIngestor{})

	resp := doReq(t, s, http.MethodPost, "/api/llm-providers/p2/activate", nil, testToken)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("activate status = %d, want 204", resp.StatusCode)
	}
	active, _ := st.GetActiveLLMProvider(t.Context())
	if active.ID != "p2" {
		t.Errorf("expected p2 active, got %s", active.ID)
	}

	resp = doReq(t, s, http.MethodPost, "/api/llm-providers/nope/activate", nil, testToken)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("activate unknown status = %d, want 404", resp.StatusCode)
	}

	resp = doReq(t, s, http.MethodDelete, "/api/llm-providers/nope", nil, testToken)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete unknown status = %d, want 404", resp.StatusCode)
	}
}

func TestLLMProviders_RequireAuth(t *testing.T) {
	s := newTestServer(t, &fakeStore{}, &fakeIngestor{})
	resp := doReq(t, s, http.MethodGet, "/api/llm-providers", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list status = %d, want 401", resp.StatusCode)
	}
}
