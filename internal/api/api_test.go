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
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gofiber/fiber/v3"

	"deep-reader/internal/auth"
	"deep-reader/internal/config"
	"deep-reader/internal/model"
	"deep-reader/internal/ports"
	"deep-reader/internal/version"
)

const testToken = "secret-token"

// Sentry config the test server advertises via GET /api/config. The backend DSN
// is masked for display; the frontend DSN is sent verbatim in ConfigResponse and
// masked in ServerInfo.
const (
	testSentryBackendDSN = "https://secretkey@example.ingest.sentry.io/9"
	testSentryDSN        = "https://public@example.ingest.sentry.io/1"
	testSentryEnv        = "test"
)

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
	rawResult      *model.ArticleRaw
	rawErr         error
	deleteErr      error
	retryErr       error
	reEnrichErr    error
	upsertApplied  bool
	upsertErr      error
	lastUpsert     model.Progress
	lastSinceMeta  time.Time
	markdownUsed   int
	setPinnedErr   error
	lastPinID      string
	lastPinned     bool

	// auth
	initialized   bool
	sessions      map[string]bool
	user          *model.User
	createUserErr error
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

func (f *fakeStore) SetFailed(context.Context, string, string, string, string) error {
	return nil
}

func (f *fakeStore) GetArticleRaw(context.Context, string) (*model.ArticleRaw, error) {
	if f.rawErr != nil {
		return nil, f.rawErr
	}
	return f.rawResult, nil
}

func (f *fakeStore) SaveEnrichment(context.Context, string, model.Enrichment, time.Time) error {
	return nil
}
func (f *fakeStore) SaveEnrichmentProgress(context.Context, string, model.Enrichment) error {
	return nil
}
func (f *fakeStore) SaveSummary(context.Context, string, string) error              { return nil }
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

func (f *fakeStore) ReEnrich(context.Context, string, string) error { return f.reEnrichErr }

func (f *fakeStore) SetPinned(_ context.Context, id string, pinned bool) error {
	f.lastPinID = id
	f.lastPinned = pinned
	return f.setPinnedErr
}

func (f *fakeStore) MarkdownUnitsUsedToday(context.Context) (int, error) {
	return f.markdownUsed, nil
}

func (f *fakeStore) TryConsumeMarkdownUnits(context.Context, int, int) (bool, int, error) {
	return true, 0, nil
}

func (f *fakeStore) RefundMarkdownUnits(context.Context, int) error { return nil }

func (f *fakeStore) IsInitialized(context.Context) (bool, error) { return f.initialized, nil }

func (f *fakeStore) CreateUser(_ context.Context, username, hash string) error {
	if f.createUserErr != nil {
		return f.createUserErr
	}
	f.user = &model.User{Username: username, PasswordHash: hash}
	f.initialized = true
	return nil
}

func (f *fakeStore) GetUser(context.Context) (*model.User, error) {
	if f.user == nil {
		return nil, ports.ErrNotFound
	}
	return f.user, nil
}

func (f *fakeStore) CreateSession(_ context.Context, tokenHash string, _ time.Time) error {
	if f.sessions == nil {
		f.sessions = map[string]bool{}
	}
	f.sessions[tokenHash] = true
	return nil
}

func (f *fakeStore) SessionExists(_ context.Context, tokenHash string) (bool, error) {
	return f.sessions[tokenHash], nil
}

func (f *fakeStore) DeleteSession(_ context.Context, tokenHash string) error {
	delete(f.sessions, tokenHash)
	return nil
}

// fakeIngestor is an in-memory ports.Ingestor.
type fakeIngestor struct {
	add          func(string) (*model.Article, error)
	addText      func(string, string) (*model.Article, error)
	retry        func(string) error
	reEnrich     func(string, string) error
	lastAddURL   string
	lastAddText  [2]string
	lastReEnrich [2]string
}

func (f *fakeIngestor) Add(_ context.Context, rawURL string) (*model.Article, error) {
	f.lastAddURL = rawURL
	if f.add != nil {
		return f.add(rawURL)
	}
	return &model.Article{ID: "art-1", Status: model.StatusQueued}, nil
}

func (f *fakeIngestor) AddText(_ context.Context, title, text string) (*model.Article, error) {
	f.lastAddText = [2]string{title, text}
	if f.addText != nil {
		return f.addText(title, text)
	}
	return &model.Article{ID: "art-1", Status: model.StatusFetched}, nil
}

func (f *fakeIngestor) Retry(_ context.Context, id string) error {
	if f.retry != nil {
		return f.retry(id)
	}
	return nil
}

func (f *fakeIngestor) ReEnrich(_ context.Context, id, mode string) error {
	f.lastReEnrich = [2]string{id, mode}
	if f.reEnrich != nil {
		return f.reEnrich(id, mode)
	}
	return nil
}

// --- helpers ---------------------------------------------------------------

func newTestServer(t *testing.T, st ports.Store, ing ports.Ingestor) *Server {
	t.Helper()
	cfg := &config.Config{
		HTTPPort:          8080,
		LogLevel:          "info",
		LogFormat:         "json",
		SentryDSN:         testSentryBackendDSN,
		SentryFrontendDSN: testSentryDSN,
		SentryEnvironment: testSentryEnv,
	}
	// Seed the common case: an initialized service with a valid session for
	// testToken, so existing protected-route tests authenticate with that bearer.
	// Auth-flow tests (setup/login) reset these fields after construction.
	if fs, ok := st.(*fakeStore); ok {
		fs.initialized = true
		if fs.sessions == nil {
			fs.sessions = map[string]bool{}
		}
		fs.sessions[auth.HashToken(testToken)] = true
	}
	siteFS := fstest.MapFS{
		"index.html":                  &fstest.MapFile{Data: []byte("<!doctype html><title>app</title>")},
		"manifest.json":               &fstest.MapFile{Data: []byte(`{"name":"app"}`)},
		"manifest.webmanifest":        &fstest.MapFile{Data: []byte(`{"name":"app"}`)},
		"service-worker.js":           &fstest.MapFile{Data: []byte("self.addEventListener('install',()=>{})")},
		"_app/start.js":               &fstest.MapFile{Data: []byte("console.log('hi')")},
		"_app/immutable/chunk.abc.js": &fstest.MapFile{Data: []byte("export{}")},
		"icons/icon-192.png":          &fstest.MapFile{Data: []byte("png")},
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
	// /api/stats is behind requireAuth; only a valid session token passes.
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
			resp := doReq(t, s, http.MethodGet, "/api/stats", nil, tc.token)
			if resp.StatusCode != tc.want {
				t.Fatalf("stats status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

// TestGetArticleRaw verifies GET /api/articles/:id/raw returns the captured raw
// LLM response, and maps a missing article to 404.
func TestGetArticleRaw(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		st := &fakeStore{
			rawResult: &model.ArticleRaw{
				ID:     "a1",
				Status: model.StatusEnrichFailed,
				Error:  "llm: unmarshal enrichment content: unexpected end of JSON input",
				Raw:    `{"sentences": [ {"start_index": 0, "end_`,
			},
		}
		s := newTestServer(t, st, &fakeIngestor{})

		resp := doReq(t, s, http.MethodGet, "/api/articles/a1/raw", nil, testToken)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		got := decode[model.ArticleRaw](t, resp)
		if got.Raw != st.rawResult.Raw {
			t.Errorf("raw = %q, want %q", got.Raw, st.rawResult.Raw)
		}
		if got.Error == "" {
			t.Error("expected error message in response")
		}
	})

	t.Run("not found", func(t *testing.T) {
		st := &fakeStore{rawErr: ports.ErrNotFound}
		s := newTestServer(t, st, &fakeIngestor{})

		resp := doReq(t, s, http.MethodGet, "/api/articles/missing/raw", nil, testToken)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})
}

// TestConfigPublicAuthFlag verifies /api/config is reachable without a token and
// reports the setup/auth flags so the client can route to /setup or /login.
func TestConfigPublicAuthFlag(t *testing.T) {
	t.Run("uninitialized", func(t *testing.T) {
		st := &fakeStore{}
		s := newTestServer(t, st, &fakeIngestor{})
		st.initialized = false
		st.sessions = map[string]bool{}

		resp := doReq(t, s, http.MethodGet, "/api/config", nil, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		got := decode[model.ConfigResponse](t, resp)
		if got.Auth.Initialized || got.Auth.Authenticated {
			t.Errorf("auth = %+v, want both false", got.Auth)
		}
	})

	t.Run("initialized but unauthenticated", func(t *testing.T) {
		st := &fakeStore{metas: []model.ArticleMeta{{ID: "a1"}}}
		s := newTestServer(t, st, &fakeIngestor{})

		resp := doReq(t, s, http.MethodGet, "/api/config", nil, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		got := decode[model.ConfigResponse](t, resp)
		if !got.Auth.Initialized || got.Auth.Authenticated {
			t.Errorf("auth = %+v, want initialized && !authenticated", got.Auth)
		}
		if len(got.Articles) != 0 {
			t.Errorf("unauthenticated config leaked %d articles", len(got.Articles))
		}
	})

	t.Run("authenticated", func(t *testing.T) {
		st := &fakeStore{metas: []model.ArticleMeta{{ID: "a1"}}}
		s := newTestServer(t, st, &fakeIngestor{})

		resp := doReq(t, s, http.MethodGet, "/api/config", nil, testToken)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		got := decode[model.ConfigResponse](t, resp)
		if !got.Auth.Initialized || !got.Auth.Authenticated {
			t.Errorf("auth = %+v, want both true", got.Auth)
		}
		if len(got.Articles) != 1 {
			t.Errorf("articles = %d, want 1", len(got.Articles))
		}
	})
}

// TestConfigSentry verifies the browser Sentry block is delivered via
// /api/config to BOTH unauthenticated and authenticated callers (so reporting
// works on /login and /setup), carrying the configured DSN/environment and the
// server version as the release.
func TestConfigSentry(t *testing.T) {
	assertSentry := func(t *testing.T, got model.SentryConfig) {
		t.Helper()
		if got.DSN != testSentryDSN {
			t.Errorf("sentry.dsn = %q, want %q", got.DSN, testSentryDSN)
		}
		if got.Environment != testSentryEnv {
			t.Errorf("sentry.environment = %q, want %q", got.Environment, testSentryEnv)
		}
		if got.Release != version.Version {
			t.Errorf("sentry.release = %q, want %q", got.Release, version.Version)
		}
	}

	t.Run("unauthenticated", func(t *testing.T) {
		st := &fakeStore{metas: []model.ArticleMeta{{ID: "a1"}}}
		s := newTestServer(t, st, &fakeIngestor{})

		resp := doReq(t, s, http.MethodGet, "/api/config", nil, "")
		got := decode[model.ConfigResponse](t, resp)
		assertSentry(t, got.Sentry)
	})

	t.Run("authenticated", func(t *testing.T) {
		st := &fakeStore{metas: []model.ArticleMeta{{ID: "a1"}}}
		s := newTestServer(t, st, &fakeIngestor{})

		resp := doReq(t, s, http.MethodGet, "/api/config", nil, testToken)
		got := decode[model.ConfigResponse](t, resp)
		assertSentry(t, got.Sentry)

		// ServerInfo carries the same vars for the settings UI, but with MASKED
		// DSNs — the raw secrets must never appear there.
		si := got.ServerInfo
		if strings.Contains(si.SentryDSN, "secretkey") || !strings.Contains(si.SentryDSN, "*") {
			t.Errorf("server_info.sentry_dsn = %q, want masked", si.SentryDSN)
		}
		if strings.Contains(si.SentryFrontendDSN, "public@") || !strings.Contains(si.SentryFrontendDSN, "*") {
			t.Errorf("server_info.sentry_frontend_dsn = %q, want masked", si.SentryFrontendDSN)
		}
		if si.SentryEnvironment != testSentryEnv {
			t.Errorf("server_info.sentry_environment = %q, want %q", si.SentryEnvironment, testSentryEnv)
		}
	})
}

func TestMaskSecret(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty stays empty", "", ""},
		{"short fully masked", "abcd", "****"},
		{"reveals last four", "abcdefgh", "****efgh"},
		{"caps the asterisk run", "0123456789abcdefghijklmnop", "************mnop"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := maskSecret(tc.in); got != tc.want {
				t.Errorf("maskSecret(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSetup(t *testing.T) {
	t.Run("first run creates user and returns token", func(t *testing.T) {
		st := &fakeStore{}
		s := newTestServer(t, st, &fakeIngestor{})
		st.initialized = false
		st.sessions = map[string]bool{}

		body := model.SetupRequest{Username: "alice", Password: "hunter2!!"}
		resp := doReq(t, s, http.MethodPost, "/api/setup", body, "")
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 201", resp.StatusCode)
		}
		got := decode[model.AuthResponse](t, resp)
		if got.Token == "" || got.Username != "alice" {
			t.Fatalf("response = %+v", got)
		}
		// The returned token must authenticate subsequent requests.
		ok, _ := st.SessionExists(context.Background(), auth.HashToken(got.Token))
		if !ok {
			t.Error("issued token was not persisted as a session")
		}
		if st.user == nil || st.user.Username != "alice" || st.user.PasswordHash == "hunter2!!" {
			t.Errorf("user not stored with a hashed password: %+v", st.user)
		}
	})

	t.Run("rejects short password", func(t *testing.T) {
		st := &fakeStore{}
		s := newTestServer(t, st, &fakeIngestor{})
		st.initialized = false

		body := model.SetupRequest{Username: "alice", Password: "short"}
		resp := doReq(t, s, http.MethodPost, "/api/setup", body, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("conflict when already initialized", func(t *testing.T) {
		st := &fakeStore{createUserErr: ports.ErrAlreadyInitialized}
		s := newTestServer(t, st, &fakeIngestor{})

		body := model.SetupRequest{Username: "alice", Password: "hunter2!!"}
		resp := doReq(t, s, http.MethodPost, "/api/setup", body, "")
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("status = %d, want 409", resp.StatusCode)
		}
	})
}

func TestLogin(t *testing.T) {
	hash, err := auth.HashPassword("hunter2!!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	t.Run("valid credentials issue a token", func(t *testing.T) {
		st := &fakeStore{user: &model.User{Username: "alice", PasswordHash: hash}}
		s := newTestServer(t, st, &fakeIngestor{})

		body := model.LoginRequest{Username: "alice", Password: "hunter2!!"}
		resp := doReq(t, s, http.MethodPost, "/api/login", body, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		got := decode[model.AuthResponse](t, resp)
		if got.Token == "" {
			t.Fatal("expected a token")
		}
		ok, _ := st.SessionExists(context.Background(), auth.HashToken(got.Token))
		if !ok {
			t.Error("issued token was not persisted as a session")
		}
	})

	t.Run("wrong password is 401", func(t *testing.T) {
		st := &fakeStore{user: &model.User{Username: "alice", PasswordHash: hash}}
		s := newTestServer(t, st, &fakeIngestor{})

		body := model.LoginRequest{Username: "alice", Password: "nope"}
		resp := doReq(t, s, http.MethodPost, "/api/login", body, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("unknown user is 401", func(t *testing.T) {
		st := &fakeStore{}
		s := newTestServer(t, st, &fakeIngestor{})
		st.user = nil

		body := model.LoginRequest{Username: "bob", Password: "whatever1"}
		resp := doReq(t, s, http.MethodPost, "/api/login", body, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("brute-force lockout after repeated failures", func(t *testing.T) {
		st := &fakeStore{user: &model.User{Username: "alice", PasswordHash: hash}}
		s := newTestServer(t, st, &fakeIngestor{})
		// Enable the guard with a small threshold (the test cfg disables it).
		s.loginGuard = newLoginGuard(3, 15*time.Minute, 15*time.Minute)

		bad := model.LoginRequest{Username: "alice", Password: "nope"}
		for i := range 3 {
			resp := doReq(t, s, http.MethodPost, "/api/login", bad, "")
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("failure %d: status = %d, want 401", i+1, resp.StatusCode)
			}
		}

		// The 4th attempt is locked out, even with the *correct* password.
		good := model.LoginRequest{Username: "alice", Password: "hunter2!!"}
		resp := doReq(t, s, http.MethodPost, "/api/login", good, "")
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("locked attempt: status = %d, want 429", resp.StatusCode)
		}
		if ra := resp.Header.Get("Retry-After"); ra == "" {
			t.Error("expected a Retry-After header on lockout")
		}
	})

	t.Run("successful login resets the failure streak", func(t *testing.T) {
		st := &fakeStore{user: &model.User{Username: "alice", PasswordHash: hash}}
		s := newTestServer(t, st, &fakeIngestor{})
		s.loginGuard = newLoginGuard(3, 15*time.Minute, 15*time.Minute)

		bad := model.LoginRequest{Username: "alice", Password: "nope"}
		for range 2 {
			doReq(t, s, http.MethodPost, "/api/login", bad, "")
		}
		// A success clears the 2 prior failures.
		good := model.LoginRequest{Username: "alice", Password: "hunter2!!"}
		if resp := doReq(t, s, http.MethodPost, "/api/login", good, ""); resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		// Two more failures must NOT lock out (streak was reset to zero).
		for i := range 2 {
			resp := doReq(t, s, http.MethodPost, "/api/login", bad, "")
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("post-reset failure %d: status = %d, want 401", i+1, resp.StatusCode)
			}
		}
	})
}

func TestLogout(t *testing.T) {
	st := &fakeStore{}
	s := newTestServer(t, st, &fakeIngestor{})

	resp := doReq(t, s, http.MethodPost, "/api/logout", nil, testToken)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	// The session must be gone after logout.
	if ok, _ := st.SessionExists(context.Background(), auth.HashToken(testToken)); ok {
		t.Error("session still present after logout")
	}
	// And a subsequent protected request with the same token is rejected.
	resp2 := doReq(t, s, http.MethodGet, "/api/stats", nil, testToken)
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("post-logout stats status = %d, want 401", resp2.StatusCode)
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

	t.Run("warn threshold out of range", func(t *testing.T) {
		s := newTestServer(t, &fakeStore{}, &fakeIngestor{})
		for _, bad := range []int{-1, model.MaxMarkdownWarnThreshold + 1} {
			resp := doReq(t, s, http.MethodPatch, "/api/settings", model.SettingsPatch{MarkdownWarnThreshold: &bad}, testToken)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("threshold %d: status = %d, want 400", bad, resp.StatusCode)
			}
		}
	})

	t.Run("warn threshold valid", func(t *testing.T) {
		var applied model.SettingsPatch
		st := &fakeStore{
			updateSettings: func(p model.SettingsPatch) (model.Settings, error) {
				applied = p
				return model.Settings{MarkdownWarnThreshold: *p.MarkdownWarnThreshold}, nil
			},
		}
		s := newTestServer(t, st, &fakeIngestor{})
		threshold := 0
		resp := doReq(t, s, http.MethodPatch, "/api/settings", model.SettingsPatch{MarkdownWarnThreshold: &threshold}, testToken)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if applied.MarkdownWarnThreshold == nil || *applied.MarkdownWarnThreshold != 0 {
			t.Errorf("patch not forwarded: %+v", applied)
		}
	})

	t.Run("enrichment prompt valid (including empty)", func(t *testing.T) {
		var applied model.SettingsPatch
		st := &fakeStore{
			updateSettings: func(p model.SettingsPatch) (model.Settings, error) {
				applied = p
				return model.Settings{EnrichmentPrompt: *p.EnrichmentPrompt}, nil
			},
		}
		s := newTestServer(t, st, &fakeIngestor{})
		// Empty string is accepted (= reset to the built-in default).
		empty := ""
		resp := doReq(t, s, http.MethodPatch, "/api/settings", model.SettingsPatch{EnrichmentPrompt: &empty}, testToken)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("empty prompt: status = %d, want 200", resp.StatusCode)
		}
		if applied.EnrichmentPrompt == nil || *applied.EnrichmentPrompt != "" {
			t.Errorf("patch not forwarded: %+v", applied)
		}
	})

	t.Run("enrichment prompt too long", func(t *testing.T) {
		s := newTestServer(t, &fakeStore{}, &fakeIngestor{})
		long := strings.Repeat("x", model.MaxEnrichmentPromptLen+1)
		resp := doReq(t, s, http.MethodPatch, "/api/settings", model.SettingsPatch{EnrichmentPrompt: &long}, testToken)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("bot-wall signatures valid (including empty)", func(t *testing.T) {
		var applied model.SettingsPatch
		st := &fakeStore{
			updateSettings: func(p model.SettingsPatch) (model.Settings, error) {
				applied = p
				return model.Settings{BotWallSignatures: *p.BotWallSignatures}, nil
			},
		}
		s := newTestServer(t, st, &fakeIngestor{})
		empty := ""
		resp := doReq(t, s, http.MethodPatch, "/api/settings", model.SettingsPatch{BotWallSignatures: &empty}, testToken)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("empty signatures: status = %d, want 200", resp.StatusCode)
		}
		if applied.BotWallSignatures == nil || *applied.BotWallSignatures != "" {
			t.Errorf("patch not forwarded: %+v", applied)
		}
	})

	t.Run("bot-wall signatures too long", func(t *testing.T) {
		s := newTestServer(t, &fakeStore{}, &fakeIngestor{})
		long := strings.Repeat("x", model.MaxBotWallSignaturesLen+1)
		resp := doReq(t, s, http.MethodPatch, "/api/settings", model.SettingsPatch{BotWallSignatures: &long}, testToken)
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

func TestReEnrich(t *testing.T) {
	t.Run("full ok", func(t *testing.T) {
		ing := &fakeIngestor{}
		s := newTestServer(t, &fakeStore{}, ing)
		resp := doReq(t, s, http.MethodPost, "/api/articles/a1/reenrich", map[string]string{"mode": "full"}, testToken)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", resp.StatusCode)
		}
		if ing.lastReEnrich != [2]string{"a1", "full"} {
			t.Errorf("ingestor got %v, want [a1 full]", ing.lastReEnrich)
		}
	})
	t.Run("topup ok", func(t *testing.T) {
		ing := &fakeIngestor{}
		s := newTestServer(t, &fakeStore{}, ing)
		resp := doReq(t, s, http.MethodPost, "/api/articles/a1/reenrich", map[string]string{"mode": "topup"}, testToken)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", resp.StatusCode)
		}
		if ing.lastReEnrich != [2]string{"a1", "topup"} {
			t.Errorf("ingestor got %v, want [a1 topup]", ing.lastReEnrich)
		}
	})
	t.Run("invalid mode", func(t *testing.T) {
		s := newTestServer(t, &fakeStore{}, &fakeIngestor{})
		resp := doReq(t, s, http.MethodPost, "/api/articles/a1/reenrich", map[string]string{"mode": "bogus"}, testToken)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})
	t.Run("not found", func(t *testing.T) {
		ing := &fakeIngestor{reEnrich: func(string, string) error { return ports.ErrNotFound }}
		s := newTestServer(t, &fakeStore{}, ing)
		resp := doReq(t, s, http.MethodPost, "/api/articles/missing/reenrich", map[string]string{"mode": "full"}, testToken)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})
}

func TestSetPinned(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		st := &fakeStore{}
		s := newTestServer(t, st, &fakeIngestor{})
		resp := doReq(t, s, http.MethodPut, "/api/articles/a1/pin", map[string]bool{"pinned": true}, testToken)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", resp.StatusCode)
		}
		if st.lastPinID != "a1" || !st.lastPinned {
			t.Errorf("store got id=%q pinned=%v, want a1/true", st.lastPinID, st.lastPinned)
		}
	})
	t.Run("unpin", func(t *testing.T) {
		st := &fakeStore{}
		s := newTestServer(t, st, &fakeIngestor{})
		resp := doReq(t, s, http.MethodPut, "/api/articles/a1/pin", map[string]bool{"pinned": false}, testToken)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", resp.StatusCode)
		}
		if st.lastPinned {
			t.Errorf("store should have been called with pinned=false")
		}
	})
	t.Run("not found", func(t *testing.T) {
		st := &fakeStore{setPinnedErr: ports.ErrNotFound}
		s := newTestServer(t, st, &fakeIngestor{})
		resp := doReq(t, s, http.MethodPut, "/api/articles/missing/pin", map[string]bool{"pinned": true}, testToken)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})
	t.Run("bad body", func(t *testing.T) {
		st := &fakeStore{}
		s := newTestServer(t, st, &fakeIngestor{})
		req, _ := http.NewRequest(http.MethodPut, "/api/articles/a1/pin", bytes.NewReader([]byte("{not json")))
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
	t.Run("requires auth", func(t *testing.T) {
		st := &fakeStore{}
		s := newTestServer(t, st, &fakeIngestor{})
		resp := doReq(t, s, http.MethodPut, "/api/articles/a1/pin", map[string]bool{"pinned": true}, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
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

// TestStaticCacheControl guards the per-path cache policy: only fingerprinted
// /_app/immutable/ assets may be cached long-term. A stale index.html or
// service-worker.js is what makes the PWA update banner reappear forever.
func TestStaticCacheControl(t *testing.T) {
	s := newTestServer(t, &fakeStore{}, &fakeIngestor{})

	cases := []struct {
		path string
		want string
	}{
		{"/_app/immutable/chunk.abc.js", "public, max-age=31536000, immutable"},
		{"/service-worker.js", "no-cache"},
		{"/manifest.webmanifest", "no-cache"},
		{"/icons/icon-192.png", "public, max-age=3600"},
		{"/", "no-cache"},               // HTML shell via IndexNames
		{"/article/abc123", "no-cache"}, // SPA fallback
	}
	for _, tc := range cases {
		resp := doReq(t, s, http.MethodGet, tc.path, nil, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", tc.path, resp.StatusCode)
		}
		if got := resp.Header.Get("Cache-Control"); got != tc.want {
			t.Errorf("%s: Cache-Control = %q, want %q", tc.path, got, tc.want)
		}
	}
}
