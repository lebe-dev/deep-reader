package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gofiber/fiber/v3"

	"deep-reader/internal/config"
	"deep-reader/internal/model"
	"deep-reader/internal/ports"
)

const testToken = "secret-token"

// --- fakes -----------------------------------------------------------------

// fakeStore is an in-memory ports.Store for handler tests. Each field lets a
// test stub the behaviour of one method; unset behaviours return zero values.
type fakeStore struct {
	settings       model.Settings
	updateSettings func(model.SettingsPatch) (model.Settings, error)
	metas          []model.ArticleMeta
	progress       []model.Progress
	payload        *model.ArticlePayload
	getPayloadErr  error
	deleteErr      error
	retryErr       error
	upsertApplied  bool
	upsertErr      error
	lastUpsert     model.Progress
	lastSinceMeta  time.Time
	markdownUsed   int
}

func (f *fakeStore) GetSettings(context.Context) (model.Settings, error) { return f.settings, nil }

func (f *fakeStore) UpdateSettings(_ context.Context, p model.SettingsPatch) (model.Settings, error) {
	if f.updateSettings != nil {
		return f.updateSettings(p)
	}
	return f.settings, nil
}

func (f *fakeStore) CreateArticle(context.Context, *model.Article) error { return nil }
func (f *fakeStore) GetArticleByHash(context.Context, string) (*model.Article, error) {
	return nil, ports.ErrNotFound
}

func (f *fakeStore) ListArticleMeta(_ context.Context, since time.Time) ([]model.ArticleMeta, error) {
	f.lastSinceMeta = since
	return f.metas, nil
}

func (f *fakeStore) GetArticle(context.Context, string) (*model.Article, error) {
	return nil, ports.ErrNotFound
}

func (f *fakeStore) GetArticlePayload(context.Context, string) (*model.ArticlePayload, error) {
	if f.getPayloadErr != nil {
		return nil, f.getPayloadErr
	}
	return f.payload, nil
}

func (f *fakeStore) DeleteArticle(context.Context, string) error { return f.deleteErr }
func (f *fakeStore) SetStatus(context.Context, string, string, string) error {
	return nil
}

func (f *fakeStore) SaveEnrichment(context.Context, string, model.Enrichment, time.Time) error {
	return nil
}
func (f *fakeStore) SaveContent(context.Context, string, ports.ContentUpdate) error { return nil }
func (f *fakeStore) ListWork(context.Context, int) ([]model.Article, error)         { return nil, nil }

func (f *fakeStore) UpsertProgress(_ context.Context, p model.Progress) (bool, error) {
	f.lastUpsert = p
	return f.upsertApplied, f.upsertErr
}

func (f *fakeStore) ListProgress(context.Context, time.Time) ([]model.Progress, error) {
	return f.progress, nil
}
func (f *fakeStore) RetryArticle(context.Context, string) error { return f.retryErr }

func (f *fakeStore) MarkdownUnitsUsedToday(context.Context) (int, error) {
	return f.markdownUsed, nil
}

func (f *fakeStore) TryConsumeMarkdownUnits(context.Context, int, int) (bool, int, error) {
	return true, 0, nil
}

func (f *fakeStore) RefundMarkdownUnits(context.Context, int) error { return nil }

// fakeIngestor is an in-memory ports.Ingestor.
type fakeIngestor struct {
	add        func(string) (*model.Article, error)
	retry      func(string) error
	lastAddURL string
}

func (f *fakeIngestor) Add(_ context.Context, rawURL string) (*model.Article, error) {
	f.lastAddURL = rawURL
	if f.add != nil {
		return f.add(rawURL)
	}
	return &model.Article{ID: "art-1", Status: model.StatusQueued}, nil
}

func (f *fakeIngestor) Retry(_ context.Context, id string) error {
	if f.retry != nil {
		return f.retry(id)
	}
	return nil
}

// --- helpers ---------------------------------------------------------------

func newTestServer(t *testing.T, st ports.Store, ing ports.Ingestor) *Server {
	t.Helper()
	cfg := &config.Config{
		AuthToken: testToken,
		HTTPPort:  8080,
		LogLevel:  "info",
		LogFormat: "json",
	}
	siteFS := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><title>app</title>")},
		"manifest.json": &fstest.MapFile{Data: []byte(`{"name":"app"}`)},
		"_app/start.js": &fstest.MapFile{Data: []byte("console.log('hi')")},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, st, ing, WithStaticFS(fs.FS(siteFS)), WithLogger(log))
}

func doReq(t *testing.T, s *Server, method, target string, body any, token string) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, target, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.App().Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return v
}

// --- tests -----------------------------------------------------------------

func TestHealthzNoAuth(t *testing.T) {
	s := newTestServer(t, &fakeStore{}, &fakeIngestor{})
	resp := doReq(t, s, http.MethodGet, "/healthz", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", resp.StatusCode)
	}
}

func TestAPIAuth(t *testing.T) {
	s := newTestServer(t, &fakeStore{}, &fakeIngestor{})

	cases := []struct {
		name  string
		token string
		want  int
	}{
		{"no token", "", http.StatusUnauthorized},
		{"bad token", "wrong", http.StatusUnauthorized},
		{"good token", testToken, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doReq(t, s, http.MethodGet, "/api/config", nil, tc.token)
			if resp.StatusCode != tc.want {
				t.Fatalf("config status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestConfigReturnsSettingsAndArticles(t *testing.T) {
	st := &fakeStore{
		settings: model.Settings{CEFRLevel: model.CEFRB1, TargetLanguage: "ru"},
		metas: []model.ArticleMeta{
			{ID: "a1", Title: "One", Status: model.StatusEnriched},
			{ID: "a2", Title: "Two", Status: model.StatusQueued},
		},
		progress: []model.Progress{{ArticleID: "a1", Position: 5}},
	}
	s := newTestServer(t, st, &fakeIngestor{})

	resp := doReq(t, s, http.MethodGet, "/api/config", nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := decode[model.ConfigResponse](t, resp)
	if got.Settings.CEFRLevel != model.CEFRB1 {
		t.Errorf("cefr = %q, want B1", got.Settings.CEFRLevel)
	}
	if len(got.Articles) != 2 {
		t.Errorf("articles = %d, want 2", len(got.Articles))
	}
	if len(got.Progress) != 1 {
		t.Errorf("progress = %d, want 1", len(got.Progress))
	}
	if got.ServerTime.IsZero() {
		t.Error("server_time should be set")
	}
}

func TestConfigSinceParsing(t *testing.T) {
	st := &fakeStore{}
	s := newTestServer(t, st, &fakeIngestor{})

	when := "2026-01-02T15:04:05Z"
	resp := doReq(t, s, http.MethodGet, "/api/config?since="+when, nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	want, _ := time.Parse(time.RFC3339, when)
	if !st.lastSinceMeta.Equal(want) {
		t.Errorf("since passed to store = %v, want %v", st.lastSinceMeta, want)
	}

	bad := doReq(t, s, http.MethodGet, "/api/config?since=not-a-time", nil, testToken)
	if bad.StatusCode != http.StatusBadRequest {
		t.Errorf("bad since status = %d, want 400", bad.StatusCode)
	}
}

func TestAddArticle(t *testing.T) {
	ing := &fakeIngestor{
		add: func(string) (*model.Article, error) {
			return &model.Article{ID: "new-id", Status: model.StatusQueued}, nil
		},
	}
	s := newTestServer(t, &fakeStore{}, ing)

	resp := doReq(t, s, http.MethodPost, "/api/articles", model.AddArticleRequest{URL: "https://example.com/x"}, testToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	got := decode[model.AddArticleResponse](t, resp)
	if got.ID != "new-id" || got.Status != model.StatusQueued {
		t.Errorf("got %+v, want {new-id pending}", got)
	}
	if ing.lastAddURL != "https://example.com/x" {
		t.Errorf("ingestor got url %q", ing.lastAddURL)
	}
}

func TestAddArticleMissingURL(t *testing.T) {
	s := newTestServer(t, &fakeStore{}, &fakeIngestor{})
	resp := doReq(t, s, http.MethodPost, "/api/articles", model.AddArticleRequest{URL: ""}, testToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAddArticleServerError(t *testing.T) {
	// Add no longer fetches, so transport/extract failures don't surface here;
	// any non-URL error from Add maps to a 500.
	ing := &fakeIngestor{
		add: func(string) (*model.Article, error) { return nil, errors.New("boom") },
	}
	s := newTestServer(t, &fakeStore{}, ing)
	resp := doReq(t, s, http.MethodPost, "/api/articles", model.AddArticleRequest{URL: "https://example.com/x"}, testToken)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}

func TestGetArticleEnriched(t *testing.T) {
	st := &fakeStore{
		payload: &model.ArticlePayload{
			ID:         "a1",
			Status:     model.StatusEnriched,
			Enrichment: &model.Enrichment{},
		},
	}
	s := newTestServer(t, st, &fakeIngestor{})
	resp := doReq(t, s, http.MethodGet, "/api/articles/a1", nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestGetArticleNotEnriched409(t *testing.T) {
	st := &fakeStore{
		payload: &model.ArticlePayload{ID: "a1", Status: model.StatusQueued},
	}
	s := newTestServer(t, st, &fakeIngestor{})
	resp := doReq(t, s, http.MethodGet, "/api/articles/a1", nil, testToken)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestGetArticleNotFound(t *testing.T) {
	st := &fakeStore{getPayloadErr: ports.ErrNotFound}
	s := newTestServer(t, st, &fakeIngestor{})
	resp := doReq(t, s, http.MethodGet, "/api/articles/missing", nil, testToken)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestProgressLWW(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	t.Run("applied", func(t *testing.T) {
		st := &fakeStore{upsertApplied: true}
		s := newTestServer(t, st, &fakeIngestor{})
		body := progressRequest{Position: 42, IsRead: true, UpdatedAt: now}
		resp := doReq(t, s, http.MethodPut, "/api/articles/a1/progress", body, testToken)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		got := decode[progressResponse](t, resp)
		if !got.Applied {
			t.Error("applied = false, want true")
		}
		if st.lastUpsert.ArticleID != "a1" || st.lastUpsert.Position != 42 || !st.lastUpsert.IsRead {
			t.Errorf("store got %+v", st.lastUpsert)
		}
	})

	t.Run("rejected by LWW", func(t *testing.T) {
		st := &fakeStore{upsertApplied: false}
		s := newTestServer(t, st, &fakeIngestor{})
		body := progressRequest{Position: 1, UpdatedAt: now}
		resp := doReq(t, s, http.MethodPut, "/api/articles/a1/progress", body, testToken)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		got := decode[progressResponse](t, resp)
		if got.Applied {
			t.Error("applied = true, want false (older record loses)")
		}
	})

	t.Run("missing updated_at", func(t *testing.T) {
		s := newTestServer(t, &fakeStore{}, &fakeIngestor{})
		body := progressRequest{Position: 1}
		resp := doReq(t, s, http.MethodPut, "/api/articles/a1/progress", body, testToken)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})
}

func TestPatchSettings(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		var applied model.SettingsPatch
		st := &fakeStore{
			updateSettings: func(p model.SettingsPatch) (model.Settings, error) {
				applied = p
				return model.Settings{CEFRLevel: *p.CEFRLevel}, nil
			},
		}
		s := newTestServer(t, st, &fakeIngestor{})
		level := model.CEFRC1
		resp := doReq(t, s, http.MethodPatch, "/api/settings", model.SettingsPatch{CEFRLevel: &level}, testToken)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if applied.CEFRLevel == nil || *applied.CEFRLevel != model.CEFRC1 {
			t.Errorf("patch not forwarded: %+v", applied)
		}
	})

	t.Run("invalid cefr", func(t *testing.T) {
		s := newTestServer(t, &fakeStore{}, &fakeIngestor{})
		bad := "Z9"
		resp := doReq(t, s, http.MethodPatch, "/api/settings", model.SettingsPatch{CEFRLevel: &bad}, testToken)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})
}

func TestDeleteArticle(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		s := newTestServer(t, &fakeStore{}, &fakeIngestor{})
		resp := doReq(t, s, http.MethodDelete, "/api/articles/a1", nil, testToken)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", resp.StatusCode)
		}
	})
	t.Run("not found", func(t *testing.T) {
		st := &fakeStore{deleteErr: ports.ErrNotFound}
		s := newTestServer(t, st, &fakeIngestor{})
		resp := doReq(t, s, http.MethodDelete, "/api/articles/missing", nil, testToken)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})
}

func TestRetry(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		s := newTestServer(t, &fakeStore{}, &fakeIngestor{})
		resp := doReq(t, s, http.MethodPost, "/api/articles/a1/retry", nil, testToken)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", resp.StatusCode)
		}
	})
	t.Run("not found", func(t *testing.T) {
		ing := &fakeIngestor{retry: func(string) error { return ports.ErrNotFound }}
		s := newTestServer(t, &fakeStore{}, ing)
		resp := doReq(t, s, http.MethodPost, "/api/articles/missing/retry", nil, testToken)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})
}

func TestStats(t *testing.T) {
	st := &fakeStore{
		metas: []model.ArticleMeta{
			{Status: model.StatusQueued},
			{Status: model.StatusEnriched},
			{Status: model.StatusEnriched},
			{Status: model.StatusFetchFailed},
		},
	}
	s := newTestServer(t, st, &fakeIngestor{})
	resp := doReq(t, s, http.MethodGet, "/api/stats", nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := decode[statsResponse](t, resp)
	if got.Total != 4 || got.InProgress != 1 || got.Ready != 2 || got.Failed != 1 {
		t.Errorf("stats = %+v", got)
	}
}

// --- static serving --------------------------------------------------------

func TestStaticServesIndexAtRoot(t *testing.T) {
	s := newTestServer(t, &fakeStore{}, &fakeIngestor{})
	resp := doReq(t, s, http.MethodGet, "/", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestStaticSPAFallback(t *testing.T) {
	s := newTestServer(t, &fakeStore{}, &fakeIngestor{})
	// A client-side route with no file extension and no real file should fall
	// back to index.html (so a hard refresh of /article/x boots the PWA).
	resp := doReq(t, s, http.MethodGet, "/article/abc123", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SPA fallback)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("<!doctype html")) {
		t.Errorf("fallback body not index.html: %q", body)
	}
}

func TestStaticMissingAssetIs404(t *testing.T) {
	s := newTestServer(t, &fakeStore{}, &fakeIngestor{})
	// A path with a file extension that does not exist must NOT fall back to
	// index.html; it is a genuine 404.
	resp := doReq(t, s, http.MethodGet, "/does-not-exist.png", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestStaticServesAsset(t *testing.T) {
	s := newTestServer(t, &fakeStore{}, &fakeIngestor{})
	resp := doReq(t, s, http.MethodGet, "/_app/start.js", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
