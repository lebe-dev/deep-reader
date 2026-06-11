package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"deep-reader/internal/config"
	"deep-reader/internal/model"
	"deep-reader/internal/ports"
	"deep-reader/internal/store"
)

func TestLLMProviders_EmptyByDefault(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	providers, err := s.ListLLMProviders(ctx)
	if err != nil {
		t.Fatalf("ListLLMProviders: %v", err)
	}
	if len(providers) != 0 {
		t.Fatalf("expected no providers without env seed, got %d", len(providers))
	}

	if _, err := s.GetActiveLLMProvider(ctx); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestLLMProviders_SeededFromEnv(t *testing.T) {
	cfg := &config.Config{
		DatabasePath:  filepath.Join(t.TempDir(), "test.db"),
		LLMAPIBaseURL: "https://api.example.com/v1",
		LLMAPIKey:     "sk-secret-1234",
		LLMModel:      "gpt-4o-mini",
	}
	s, err := store.NewSQLite(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	active, err := s.GetActiveLLMProvider(ctx)
	if err != nil {
		t.Fatalf("GetActiveLLMProvider: %v", err)
	}
	if active.BaseURL != cfg.LLMAPIBaseURL || active.APIKey != cfg.LLMAPIKey || active.Model != cfg.LLMModel {
		t.Fatalf("seeded provider mismatch: %+v", active)
	}
	if !active.IsActive {
		t.Fatalf("seeded provider should be active")
	}
}

func TestLLMProviders_FirstBecomesActive(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	first, err := s.CreateLLMProvider(ctx, model.LLMProvider{Name: "A", BaseURL: "https://a", Model: "m"})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	if !first.IsActive {
		t.Fatalf("first provider should be active")
	}

	second, err := s.CreateLLMProvider(ctx, model.LLMProvider{Name: "B", BaseURL: "https://b", Model: "m"})
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	if second.IsActive {
		t.Fatalf("second provider should not auto-activate")
	}
}

func TestLLMProviders_SetActiveIsExclusive(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	a, _ := s.CreateLLMProvider(ctx, model.LLMProvider{Name: "A", BaseURL: "https://a", Model: "m"})
	b, _ := s.CreateLLMProvider(ctx, model.LLMProvider{Name: "B", BaseURL: "https://b", Model: "m"})

	if err := s.SetActiveLLMProvider(ctx, b.ID); err != nil {
		t.Fatalf("SetActive B: %v", err)
	}

	active, err := s.GetActiveLLMProvider(ctx)
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	if active.ID != b.ID {
		t.Fatalf("expected B active, got %s", active.ID)
	}

	providers, _ := s.ListLLMProviders(ctx)
	var activeCount int
	for _, p := range providers {
		if p.IsActive {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Fatalf("expected exactly one active provider, got %d", activeCount)
	}
	_ = a

	if err := s.SetActiveLLMProvider(ctx, "nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown id, got %v", err)
	}
}

func TestLLMProviders_UpdateKeepsKeyWhenNil(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	p, _ := s.CreateLLMProvider(ctx, model.LLMProvider{Name: "A", BaseURL: "https://a", APIKey: "sk-old", Model: "m"})

	// Nil APIKey => keep stored secret.
	updated, err := s.UpdateLLMProvider(ctx, p.ID, model.LLMProviderInput{Name: "A2", BaseURL: "https://a2", Model: "m2"})
	if err != nil {
		t.Fatalf("update keep key: %v", err)
	}
	if updated.APIKey != "sk-old" {
		t.Fatalf("expected key preserved, got %q", updated.APIKey)
	}
	if updated.Name != "A2" || updated.BaseURL != "https://a2" || updated.Model != "m2" {
		t.Fatalf("non-secret fields not applied: %+v", updated)
	}

	// Non-nil APIKey => replace.
	newKey := "sk-new"
	updated, err = s.UpdateLLMProvider(ctx, p.ID, model.LLMProviderInput{Name: "A2", BaseURL: "https://a2", Model: "m2", APIKey: &newKey})
	if err != nil {
		t.Fatalf("update replace key: %v", err)
	}
	if updated.APIKey != "sk-new" {
		t.Fatalf("expected key replaced, got %q", updated.APIKey)
	}
}

func TestLLMProviders_ForceJSONObjectRoundTrip(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// Defaults to false on create.
	p, err := s.CreateLLMProvider(ctx, model.LLMProvider{Name: "A", BaseURL: "https://a", Model: "m"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.ForceJSONObject {
		t.Fatalf("expected ForceJSONObject false by default, got true")
	}

	// Update toggles it on and it survives a re-read.
	updated, err := s.UpdateLLMProvider(ctx, p.ID, model.LLMProviderInput{Name: "A", BaseURL: "https://a", Model: "m", ForceJSONObject: true})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !updated.ForceJSONObject {
		t.Fatalf("expected ForceJSONObject true after update, got false")
	}
	got, err := s.GetActiveLLMProvider(ctx)
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	if !got.ForceJSONObject {
		t.Fatalf("expected ForceJSONObject true after re-read, got false")
	}
}

func TestLLMProviders_DeletePromotesActive(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	a, _ := s.CreateLLMProvider(ctx, model.LLMProvider{Name: "A", BaseURL: "https://a", Model: "m"})
	b, _ := s.CreateLLMProvider(ctx, model.LLMProvider{Name: "B", BaseURL: "https://b", Model: "m"})

	// A is active (first). Delete it; B should be promoted.
	if err := s.DeleteLLMProvider(ctx, a.ID); err != nil {
		t.Fatalf("delete A: %v", err)
	}
	active, err := s.GetActiveLLMProvider(ctx)
	if err != nil {
		t.Fatalf("GetActive after delete: %v", err)
	}
	if active.ID != b.ID {
		t.Fatalf("expected B promoted to active, got %s", active.ID)
	}

	// Delete the last one; no active remains.
	if err := s.DeleteLLMProvider(ctx, b.ID); err != nil {
		t.Fatalf("delete B: %v", err)
	}
	if _, err := s.GetActiveLLMProvider(ctx); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after deleting all, got %v", err)
	}

	if err := s.DeleteLLMProvider(ctx, "nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("expected ErrNotFound deleting unknown, got %v", err)
	}
}

// TestLLMProviders_SeedSkippedWhenEnvIncomplete verifies the tightened seed
// guard: partial LLM_* env config (only an API key, or only a base URL) must
// NOT seed a profile, because such a profile would be missing base_url/model and
// later fail LLMProviderInput.Validate() on every call. The seed is skipped
// (with a warning) and the table stays empty so the user configures it in the UI.
func TestLLMProviders_SeedSkippedWhenEnvIncomplete(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
	}{
		{"only api key", &config.Config{LLMAPIKey: "sk-secret"}},
		{"only base url", &config.Config{LLMAPIBaseURL: "https://api.example.com/v1"}},
		{"only model", &config.Config{LLMModel: "gpt-4o-mini"}},
		{"key + model, no base url", &config.Config{LLMAPIKey: "sk", LLMModel: "gpt-4o-mini"}},
		{"key + base url, no model", &config.Config{LLMAPIKey: "sk", LLMAPIBaseURL: "https://api.example.com/v1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.cfg.DatabasePath = filepath.Join(t.TempDir(), "test.db")
			s, err := store.NewSQLite(context.Background(), tc.cfg)
			if err != nil {
				t.Fatalf("NewSQLite: %v", err)
			}
			t.Cleanup(func() { _ = s.Close() })

			providers, err := s.ListLLMProviders(context.Background())
			if err != nil {
				t.Fatalf("ListLLMProviders: %v", err)
			}
			if len(providers) != 0 {
				t.Fatalf("incomplete env config should not seed a provider, got %+v", providers)
			}
		})
	}
}

// TestLLMProviders_SeedKeylessWhenComplete verifies the seed still works for a
// keyless local provider (e.g. Ollama): base_url + model present, no API key.
// This passes Validate(), so seeding is correct.
func TestLLMProviders_SeedKeylessWhenComplete(t *testing.T) {
	cfg := &config.Config{
		DatabasePath:  filepath.Join(t.TempDir(), "test.db"),
		LLMAPIBaseURL: "http://localhost:11434/v1",
		LLMModel:      "llama3",
	}
	s, err := store.NewSQLite(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	active, err := s.GetActiveLLMProvider(context.Background())
	if err != nil {
		t.Fatalf("GetActiveLLMProvider: %v", err)
	}
	if active.BaseURL != cfg.LLMAPIBaseURL || active.Model != cfg.LLMModel {
		t.Fatalf("keyless seed mismatch: %+v", active)
	}
	if active.APIKey != "" {
		t.Fatalf("keyless provider should have empty key, got %q", active.APIKey)
	}
}

// TestLLMProviders_DeleteInactiveKeepsActive verifies the transactional delete
// path for a non-active provider: deleting an inactive profile must not disturb
// the active one (no spurious promote), and the active provider is unchanged.
func TestLLMProviders_DeleteInactiveKeepsActive(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	a, _ := s.CreateLLMProvider(ctx, model.LLMProvider{Name: "A", BaseURL: "https://a", Model: "m"}) // active (first)
	b, _ := s.CreateLLMProvider(ctx, model.LLMProvider{Name: "B", BaseURL: "https://b", Model: "m"}) // inactive

	if err := s.DeleteLLMProvider(ctx, b.ID); err != nil {
		t.Fatalf("delete inactive B: %v", err)
	}

	active, err := s.GetActiveLLMProvider(ctx)
	if err != nil {
		t.Fatalf("GetActiveLLMProvider: %v", err)
	}
	if active.ID != a.ID {
		t.Fatalf("active should remain A after deleting inactive B, got %s", active.ID)
	}

	// Exactly one provider remains and it is the still-active A.
	providers, err := s.ListLLMProviders(ctx)
	if err != nil {
		t.Fatalf("ListLLMProviders: %v", err)
	}
	if len(providers) != 1 || providers[0].ID != a.ID || !providers[0].IsActive {
		t.Fatalf("expected only A remaining and active, got %+v", providers)
	}
}

// TestLLMProviders_DeleteActivePromotesTransactionally exercises the active-delete
// promote: deleting the active provider promotes the newest remaining one, and
// promotion is atomic with the delete (a single tx) so an active connection
// always exists while any profile remains.
func TestLLMProviders_DeleteActivePromotesTransactionally(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	a, _ := s.CreateLLMProvider(ctx, model.LLMProvider{Name: "A", BaseURL: "https://a", Model: "m"})
	b, _ := s.CreateLLMProvider(ctx, model.LLMProvider{Name: "B", BaseURL: "https://b", Model: "m"})
	c, _ := s.CreateLLMProvider(ctx, model.LLMProvider{Name: "C", BaseURL: "https://c", Model: "m"})

	// A is active (first). Delete it; the newest remaining (C) is promoted.
	if err := s.DeleteLLMProvider(ctx, a.ID); err != nil {
		t.Fatalf("delete active A: %v", err)
	}
	active, err := s.GetActiveLLMProvider(ctx)
	if err != nil {
		t.Fatalf("GetActiveLLMProvider: %v", err)
	}
	if active.ID != c.ID {
		t.Fatalf("expected newest remaining (C) promoted, got %s", active.ID)
	}

	// Exactly one active remains across the table (the promote did not duplicate).
	providers, _ := s.ListLLMProviders(ctx)
	var activeCount int
	for _, p := range providers {
		if p.IsActive {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Fatalf("expected exactly one active provider after promote, got %d", activeCount)
	}
	_ = b
}

func TestMaskSecret(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"abc":          "•••",
		"sk-secret-99": "••••t-99",
	}
	for in, want := range cases {
		if got := model.MaskSecret(in); got != want {
			t.Errorf("MaskSecret(%q) = %q, want %q", in, got, want)
		}
	}
}
