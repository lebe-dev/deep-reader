package ingest_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"deep-reader/internal/config"
	"deep-reader/internal/ingest"
	"deep-reader/internal/model"
	"deep-reader/internal/ports"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeStore is a minimal in-memory implementation of ports.Store for tests.
type fakeStore struct {
	articles map[string]*model.Article // keyed by url_hash
	byID     map[string]*model.Article // keyed by id
	retried  []string                  // ids passed to RetryArticle
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		articles: make(map[string]*model.Article),
		byID:     make(map[string]*model.Article),
	}
}

func (f *fakeStore) GetArticleByHash(_ context.Context, urlHash string) (*model.Article, error) {
	a, ok := f.articles[urlHash]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return a, nil
}

func (f *fakeStore) CreateArticle(_ context.Context, a *model.Article) error {
	if _, ok := f.articles[a.URLHash]; ok {
		return ports.ErrDuplicate
	}
	copy := *a
	f.articles[a.URLHash] = &copy
	f.byID[a.ID] = &copy
	return nil
}

func (f *fakeStore) RetryArticle(_ context.Context, id string) error {
	a, ok := f.byID[id]
	if !ok {
		return ports.ErrNotFound
	}
	if a.Status == model.StatusEnrichFailed {
		a.Status = model.StatusFetched
	} else {
		a.Status = model.StatusQueued
	}
	f.retried = append(f.retried, id)
	return nil
}

func (f *fakeStore) ReEnrich(_ context.Context, id, mode string) error {
	a, ok := f.byID[id]
	if !ok {
		return ports.ErrNotFound
	}
	if mode == model.ReEnrichModeTopup {
		a.Status = model.StatusTopupQueued
	} else {
		a.Status = model.StatusFetched
	}
	return nil
}

func (f *fakeStore) SetPinned(_ context.Context, id string, pinned bool) error {
	a, ok := f.byID[id]
	if !ok {
		return ports.ErrNotFound
	}
	a.Pinned = pinned
	return nil
}

func (f *fakeStore) SaveContent(_ context.Context, id string, c ports.ContentUpdate) error {
	a, ok := f.byID[id]
	if !ok {
		return ports.ErrNotFound
	}
	a.OriginalText = c.Text
	a.Tokens = c.Tokens
	a.Status = model.StatusFetched
	return nil
}

// Stub out the remaining ports.Store methods (not exercised by ingest tests).
func (f *fakeStore) GetSettings(_ context.Context) (model.Settings, error) {
	return model.Settings{}, nil
}
func (f *fakeStore) UpdateSettings(_ context.Context, _ model.SettingsPatch) (model.Settings, error) {
	return model.Settings{}, nil
}
func (f *fakeStore) ListArticleMeta(_ context.Context, _ time.Time) ([]model.ArticleMeta, error) {
	return nil, nil
}
func (f *fakeStore) GetArticle(_ context.Context, id string) (*model.Article, error) {
	a, ok := f.byID[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return a, nil
}
func (f *fakeStore) GetArticlePayload(_ context.Context, _ string) (*model.ArticlePayload, error) {
	return nil, ports.ErrNotFound
}
func (f *fakeStore) DeleteArticle(_ context.Context, _ string) error { return nil }
func (f *fakeStore) SetStatus(_ context.Context, _, _, _ string) error {
	return nil
}
func (f *fakeStore) SaveEnrichment(_ context.Context, _ string, _ model.Enrichment, _ time.Time) error {
	return nil
}
func (f *fakeStore) ListWork(_ context.Context, _ int) ([]model.Article, error) { return nil, nil }
func (f *fakeStore) UpsertProgress(_ context.Context, _ model.Progress) (bool, error) {
	return false, nil
}
func (f *fakeStore) ListProgress(_ context.Context, _ time.Time) ([]model.Progress, error) {
	return nil, nil
}
func (f *fakeStore) MarkdownUnitsUsedToday(_ context.Context) (int, error) { return 0, nil }
func (f *fakeStore) TryConsumeMarkdownUnits(_ context.Context, _, _ int) (bool, int, error) {
	return true, 0, nil
}
func (f *fakeStore) RefundMarkdownUnits(_ context.Context, _ int) error { return nil }

func (f *fakeStore) IsInitialized(_ context.Context) (bool, error)   { return true, nil }
func (f *fakeStore) CreateUser(_ context.Context, _, _ string) error { return nil }
func (f *fakeStore) GetUser(_ context.Context) (*model.User, error)  { return nil, ports.ErrNotFound }
func (f *fakeStore) CreateSession(_ context.Context, _ string, _ time.Time) error {
	return nil
}
func (f *fakeStore) SessionExists(_ context.Context, _ string) (bool, error) { return false, nil }
func (f *fakeStore) DeleteSession(_ context.Context, _ string) error         { return nil }

// fakeWorker counts Notify calls.
type fakeWorker struct {
	notified atomic.Int32
}

func (f *fakeWorker) Start(_ context.Context) {}
func (f *fakeWorker) Notify()                 { f.notified.Add(1) }

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func defaultCfg() *config.Config {
	return &config.Config{
		EnrichmentVersion:  1,
		ReadabilityTimeout: 20 * time.Second,
		LLMAPIKey:          "test",
		DatabasePath:       "/tmp/test.db",
		HTTPPort:           8080,
		LLMMaxConcurrent:   2,
		LLMMaxRetries:      3,
		LLMRequestTimeout:  60 * time.Second,
		LogLevel:           "info",
		LogFormat:          "json",
	}
}

// ---------------------------------------------------------------------------
// Tests: Add — new URL
// ---------------------------------------------------------------------------

func TestAdd_NewURL(t *testing.T) {
	st := newFakeStore()
	wk := &fakeWorker{}
	cfg := defaultCfg()

	ing := ingest.New(cfg, st, wk)

	rawURL := "https://example.com/article?utm_source=twitter#section"
	art, err := ing.Add(context.Background(), rawURL)
	if err != nil {
		t.Fatalf("Add: unexpected error: %v", err)
	}

	// Worker must have been notified once (so it picks up the fetch stage).
	if n := wk.notified.Load(); n != 1 {
		t.Errorf("worker notified: got %d, want 1", n)
	}

	// Add does not fetch: the article starts queued with no content/tokens yet.
	if art.Status != model.StatusQueued {
		t.Errorf("status: got %q, want %q", art.Status, model.StatusQueued)
	}
	if len(art.Tokens) != 0 {
		t.Errorf("tokens must be empty before fetch, got %d", len(art.Tokens))
	}

	// Article must be persisted under the normalized URL hash.
	hash := ingest.URLHash(mustNormalize(t, rawURL))
	stored, err := st.GetArticleByHash(context.Background(), hash)
	if err != nil {
		t.Fatalf("GetArticleByHash: %v", err)
	}
	if stored.ID != art.ID {
		t.Errorf("stored id mismatch: got %q, want %q", stored.ID, art.ID)
	}

	// EnrichmentVersion must match config.
	if art.EnrichmentVersion != cfg.EnrichmentVersion {
		t.Errorf("enrichment_version: got %d, want %d", art.EnrichmentVersion, cfg.EnrichmentVersion)
	}
}

func TestAdd_InvalidURLCreatesNoRecord(t *testing.T) {
	st := newFakeStore()
	wk := &fakeWorker{}
	ing := ingest.New(defaultCfg(), st, wk)

	if _, err := ing.Add(context.Background(), "/relative/path"); err == nil {
		t.Fatal("Add: expected error for URL without host")
	}
	if len(st.byID) != 0 {
		t.Errorf("no article should be created for an invalid URL, got %d", len(st.byID))
	}
	if n := wk.notified.Load(); n != 0 {
		t.Errorf("worker must not be notified on invalid URL, got %d", n)
	}
}

// ---------------------------------------------------------------------------
// Tests: Add — dedup (same hash, same enrichment version)
// ---------------------------------------------------------------------------

func TestAdd_DedupReturnExisting(t *testing.T) {
	st := newFakeStore()
	wk := &fakeWorker{}
	cfg := defaultCfg()

	ing := ingest.New(cfg, st, wk)

	rawURL := "https://example.com/article"

	// First call: ingests normally.
	first, err := ing.Add(context.Background(), rawURL)
	if err != nil {
		t.Fatalf("first Add: %v", err)
	}

	// Reset counter for the second call.
	wk.notified.Store(0)

	// Second call with same URL and same enrichment version → dedup.
	second, err := ing.Add(context.Background(), rawURL)
	if err != nil {
		t.Fatalf("second Add (dedup): %v", err)
	}

	// Worker must NOT have been notified on dedup.
	if n := wk.notified.Load(); n != 0 {
		t.Errorf("worker notified on dedup: got %d, want 0", n)
	}

	// Returned article must be the same (same id).
	if first.ID != second.ID {
		t.Errorf("dedup: ids differ: first=%q second=%q", first.ID, second.ID)
	}
}

// ---------------------------------------------------------------------------
// Tests: Retry
// ---------------------------------------------------------------------------

func TestRetry_RequeuesAndNotifies(t *testing.T) {
	st := newFakeStore()
	wk := &fakeWorker{}
	cfg := defaultCfg()

	ing := ingest.New(cfg, st, wk)

	// First, ingest an article so we have an id to retry.
	art, err := ing.Add(context.Background(), "https://example.com/article")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Reset worker counter.
	wk.notified.Store(0)

	if err := ing.Retry(context.Background(), art.ID); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	// Store must have recorded the retry.
	if len(st.retried) == 0 || st.retried[len(st.retried)-1] != art.ID {
		t.Errorf("RetryArticle not called for id %q; retried=%v", art.ID, st.retried)
	}

	// Worker must have been notified.
	if n := wk.notified.Load(); n != 1 {
		t.Errorf("worker notified after retry: got %d, want 1", n)
	}
}

func TestRetry_NotFound(t *testing.T) {
	st := newFakeStore()
	wk := &fakeWorker{}
	cfg := defaultCfg()

	ing := ingest.New(cfg, st, wk)

	err := ing.Retry(context.Background(), "nonexistent-id")
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("Retry unknown id: got %v, want ErrNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: NormalizeURL
// ---------------------------------------------------------------------------

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "strips fragment",
			input: "https://example.com/path#section",
			want:  "https://example.com/path",
		},
		{
			name:  "strips utm params",
			input: "https://example.com/path?utm_source=twitter&utm_medium=social&id=42",
			want:  "https://example.com/path?id=42",
		},
		{
			name:  "strips utm and fragment",
			input: "https://example.com/path?utm_campaign=x#top",
			want:  "https://example.com/path",
		},
		{
			name:  "lowercases host",
			input: "https://EXAMPLE.COM/Path",
			want:  "https://example.com/Path",
		},
		{
			name:  "lowercases scheme",
			input: "HTTP://example.com/path",
			want:  "http://example.com/path",
		},
		{
			name:  "preserves non-utm query params",
			input: "https://example.com/?page=2&sort=asc",
			want:  "https://example.com/?page=2&sort=asc",
		},
		{
			name:  "no query no fragment",
			input: "https://example.com/article/hello-world",
			want:  "https://example.com/article/hello-world",
		},
		{
			name:    "empty URL",
			input:   "",
			wantErr: true,
		},
		{
			name:    "no host",
			input:   "/relative/path",
			wantErr: true,
		},
		{
			name:  "utm case-insensitive strip",
			input: "https://example.com/?UTM_SOURCE=x&keep=1",
			want:  "https://example.com/?keep=1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ingest.NormalizeURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("NormalizeURL(%q): expected error, got %q", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeURL(%q): unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("NormalizeURL(%q):\n  got  %q\n  want %q", tc.input, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tests: URLHash determinism
// ---------------------------------------------------------------------------

func TestURLHash_Deterministic(t *testing.T) {
	url := "https://example.com/article"
	h1 := ingest.URLHash(url)
	h2 := ingest.URLHash(url)
	if h1 != h2 {
		t.Errorf("URLHash not deterministic: %q vs %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("URLHash length: got %d, want 64 (hex SHA-256)", len(h1))
	}
}

func TestURLHash_DifferentInputs(t *testing.T) {
	h1 := ingest.URLHash("https://example.com/a")
	h2 := ingest.URLHash("https://example.com/b")
	if h1 == h2 {
		t.Error("URLHash: different URLs produced same hash")
	}
}

// Note: extraction (and its HTML-entity decoding / error handling) now happens
// in the enrichment worker's fetch stage, not in ingest.Add — those behaviours
// are covered by the enrich package tests.

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mustNormalize(t *testing.T, rawURL string) string {
	t.Helper()
	n, err := ingest.NormalizeURL(rawURL)
	if err != nil {
		t.Fatalf("NormalizeURL(%q): %v", rawURL, err)
	}
	return n
}
