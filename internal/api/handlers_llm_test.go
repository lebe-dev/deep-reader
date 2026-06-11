package api

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

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

// TestLLMProviders_Update covers PATCH /api/llm-providers/:id. The api_key is
// write-only: omitting it must preserve the stored secret, while sending a new
// value must replace it — a regression here could leak or silently wipe the key.
// It also checks the 400 (invalid body / validation) and 404 (unknown id) maps.
func TestLLMProviders_Update(t *testing.T) {
	const existingKey = "sk-original-9999"

	newStore := func() *fakeStore {
		return &fakeStore{providers: []model.LLMProvider{
			{ID: "p1", Name: "A", BaseURL: "https://a/v1", Model: "m", APIKey: existingKey, IsActive: true},
		}}
	}

	t.Run("omitted api_key preserves the stored key", func(t *testing.T) {
		st := newStore()
		s := newTestServer(t, st, &fakeIngestor{})

		// No api_key field at all (write-only): the secret must be untouched.
		body := model.LLMProviderInput{Name: "A renamed", BaseURL: "https://a/v2", Model: "m2"}
		resp := doReq(t, s, http.MethodPatch, "/api/llm-providers/p1", body, testToken)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		view := decode[model.LLMProviderView](t, resp)
		if !view.HasKey {
			t.Errorf("has_key = false, want true (key should be preserved)")
		}
		if st.providers[0].APIKey != existingKey {
			t.Errorf("stored key = %q, want preserved %q", st.providers[0].APIKey, existingKey)
		}
		// The non-secret fields must still update.
		if st.providers[0].Name != "A renamed" || st.providers[0].BaseURL != "https://a/v2" {
			t.Errorf("non-secret fields not updated: %+v", st.providers[0])
		}
		// The response must never carry the raw key.
		if view.KeyPreview == existingKey {
			t.Errorf("response leaked the raw key")
		}
	})

	t.Run("new api_key replaces the stored key", func(t *testing.T) {
		st := newStore()
		s := newTestServer(t, st, &fakeIngestor{})

		key := "sk-replaced-0000"
		body := model.LLMProviderInput{Name: "A", BaseURL: "https://a/v1", Model: "m", APIKey: &key}
		resp := doReq(t, s, http.MethodPatch, "/api/llm-providers/p1", body, testToken)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if st.providers[0].APIKey != key {
			t.Errorf("stored key = %q, want replaced %q", st.providers[0].APIKey, key)
		}
	})

	t.Run("invalid body is 400", func(t *testing.T) {
		st := newStore()
		s := newTestServer(t, st, &fakeIngestor{})

		req, _ := http.NewRequest(http.MethodPatch, "/api/llm-providers/p1", bytes.NewReader([]byte("{not json")))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+testToken)
		resp, err := s.App().Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("validation failure is 400", func(t *testing.T) {
		st := newStore()
		s := newTestServer(t, st, &fakeIngestor{})

		// Missing base_url fails Validate -> 400 before the store is touched.
		body := model.LLMProviderInput{Name: "A", Model: "m"}
		resp := doReq(t, s, http.MethodPatch, "/api/llm-providers/p1", body, testToken)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		// The stored key must be untouched on a rejected update.
		if st.providers[0].APIKey != existingKey {
			t.Errorf("rejected update wiped the key: %q", st.providers[0].APIKey)
		}
	})

	t.Run("unknown id is 404", func(t *testing.T) {
		st := newStore()
		s := newTestServer(t, st, &fakeIngestor{})

		body := model.LLMProviderInput{Name: "A", BaseURL: "https://a/v1", Model: "m"}
		resp := doReq(t, s, http.MethodPatch, "/api/llm-providers/nope", body, testToken)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("requires auth", func(t *testing.T) {
		st := newStore()
		s := newTestServer(t, st, &fakeIngestor{})
		body := model.LLMProviderInput{Name: "A", BaseURL: "https://a/v1", Model: "m"}
		resp := doReq(t, s, http.MethodPatch, "/api/llm-providers/p1", body, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})
}
