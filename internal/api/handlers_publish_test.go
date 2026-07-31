package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"deep-reader/internal/config"
	"deep-reader/internal/model"
	"deep-reader/internal/publish"
	"deep-reader/internal/tokenize"
)

// publishableStore returns a store holding one enriched, publishable article.
func publishableStore() *fakeStore {
	text := "The engine was rewritten. Nobody expected the result."
	tokens := tokenize.Tokenize(text)
	mid := len(tokens) / 2

	return &fakeStore{
		settings: model.Settings{
			TargetLanguage:     "ru",
			PublicPageTTLHours: model.DefaultPublicPageTTLHours,
		},
		article: &model.Article{
			ID:           "art-1",
			SourceURL:    "https://example.com/post",
			SourceDomain: "example.com",
			Title:        "Rewriting the engine",
			Author:       "Jane Doe",
			Lang:         "en",
			Summary:      "How the rewrite went.",
		},
		payload: &model.ArticlePayload{
			ID:           "art-1",
			Status:       model.StatusEnriched,
			Title:        "Rewriting the engine",
			OriginalText: text,
			Tokens:       tokens,
			Enrichment: &model.Enrichment{Sentences: []model.Sentence{
				{StartIndex: 0, EndIndex: mid - 1, Translation: "Движок переписали."},
				{StartIndex: mid, EndIndex: len(tokens) - 1, Translation: "Результата никто не ждал."},
			}},
		},
	}
}

// newPublishServer builds a server whose public pages land in a temp directory.
func newPublishServer(t *testing.T, st *fakeStore, tweak func(*config.Config)) *Server {
	t.Helper()
	dir := t.TempDir()
	return newTestServerCfg(t, st, &fakeIngestor{}, func(cfg *config.Config) {
		cfg.PublicPagesDir = dir
		if tweak != nil {
			tweak(cfg)
		}
	})
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestPublishArticleCreatesAReachablePage(t *testing.T) {
	st := publishableStore()
	s := newPublishServer(t, st, nil)

	before := time.Now().UTC()
	resp := doReq(t, s, http.MethodPost, "/api/articles/art-1/publish",
		model.PublishRequest{Title: "Свой заголовок", Description: "Своё описание"}, testToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("publish status = %d, want 201: %s", resp.StatusCode, readBody(t, resp))
	}

	pub := decode[model.Publication](t, resp)
	if !publish.ValidToken(pub.Token) {
		t.Fatalf("publish returned token %q, which is not well-formed", pub.Token)
	}
	if !strings.HasSuffix(pub.URL, "/p/"+pub.Token) {
		t.Fatalf("publish returned URL %q, want it to end in /p/%s", pub.URL, pub.Token)
	}
	wantExpiry := before.Add(time.Duration(model.DefaultPublicPageTTLHours) * time.Hour)
	if pub.ExpiresAt.Before(wantExpiry.Add(-time.Minute)) || pub.ExpiresAt.After(wantExpiry.Add(time.Minute)) {
		t.Fatalf("ExpiresAt = %v, want about %v", pub.ExpiresAt, wantExpiry)
	}

	// The page must be readable with no credentials at all.
	page := doReq(t, s, http.MethodGet, "/p/"+pub.Token, nil, "")
	if page.StatusCode != http.StatusOK {
		t.Fatalf("public page status = %d, want 200", page.StatusCode)
	}
	if ct := page.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("public page Content-Type = %q, want text/html", ct)
	}

	body := readBody(t, page)
	for _, want := range []string{
		`<meta property="og:title" content="Свой заголовок">`,
		`<meta property="og:description" content="Своё описание">`,
		`<meta property="og:url" content="` + pub.URL + `">`,
		"Движок переписали.",
		"Результата никто не ждал.",
		"example.com",
		"Jane Doe",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("published page is missing %q", want)
		}
	}
}

func TestPublishArticleFallsBackToArticleMetadata(t *testing.T) {
	st := publishableStore()
	s := newPublishServer(t, st, nil)

	resp := doReq(t, s, http.MethodPost, "/api/articles/art-1/publish", model.PublishRequest{}, testToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("publish status = %d, want 201", resp.StatusCode)
	}

	pub := decode[model.Publication](t, resp)
	if pub.Title != "Rewriting the engine" {
		t.Errorf("Title = %q, want the article title", pub.Title)
	}
	if pub.Description != "How the rewrite went." {
		t.Errorf("Description = %q, want the article summary", pub.Description)
	}
}

func TestPublishArticleUsesTheConfiguredPublicBaseURL(t *testing.T) {
	st := publishableStore()
	s := newPublishServer(t, st, func(cfg *config.Config) { cfg.PublicBaseURL = "https://reader.example" })

	resp := doReq(t, s, http.MethodPost, "/api/articles/art-1/publish", model.PublishRequest{}, testToken)
	pub := decode[model.Publication](t, resp)

	if !strings.HasPrefix(pub.URL, "https://reader.example/p/") {
		t.Fatalf("URL = %q, want it built from PUBLIC_BASE_URL", pub.URL)
	}
}

func TestPublishArticleRejectsUnenrichedAndUnknownArticles(t *testing.T) {
	t.Run("not enriched", func(t *testing.T) {
		st := publishableStore()
		st.payload.Status = model.StatusFetched
		st.payload.Enrichment = nil
		s := newPublishServer(t, st, nil)

		resp := doReq(t, s, http.MethodPost, "/api/articles/art-1/publish", model.PublishRequest{}, testToken)
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("status = %d, want 409", resp.StatusCode)
		}
	})

	t.Run("unknown article", func(t *testing.T) {
		st := publishableStore()
		st.article = nil
		s := newPublishServer(t, st, nil)

		resp := doReq(t, s, http.MethodPost, "/api/articles/nope/publish", model.PublishRequest{}, testToken)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("no title anywhere", func(t *testing.T) {
		st := publishableStore()
		st.article.Title = ""
		s := newPublishServer(t, st, nil)

		resp := doReq(t, s, http.MethodPost, "/api/articles/art-1/publish", model.PublishRequest{}, testToken)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("title too long", func(t *testing.T) {
		st := publishableStore()
		s := newPublishServer(t, st, nil)

		long := strings.Repeat("я", model.MaxPublicationTitleLen+1)
		resp := doReq(t, s, http.MethodPost, "/api/articles/art-1/publish", model.PublishRequest{Title: long}, testToken)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})
}

func TestPublishArticleRequiresAuth(t *testing.T) {
	s := newPublishServer(t, publishableStore(), nil)

	resp := doReq(t, s, http.MethodPost, "/api/articles/art-1/publish", model.PublishRequest{}, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestRepublishingSupersedesTheOldLink(t *testing.T) {
	st := publishableStore()
	s := newPublishServer(t, st, nil)

	first := decode[model.Publication](t, doReq(t, s, http.MethodPost, "/api/articles/art-1/publish", model.PublishRequest{}, testToken))
	second := decode[model.Publication](t, doReq(t, s, http.MethodPost, "/api/articles/art-1/publish", model.PublishRequest{}, testToken))

	if first.Token == second.Token {
		t.Fatalf("re-publishing reused token %q; it must mint a new one", first.Token)
	}
	if got := doReq(t, s, http.MethodGet, "/p/"+first.Token, nil, "").StatusCode; got != http.StatusNotFound {
		t.Fatalf("superseded link status = %d, want 404", got)
	}
	if got := doReq(t, s, http.MethodGet, "/p/"+second.Token, nil, "").StatusCode; got != http.StatusOK {
		t.Fatalf("current link status = %d, want 200", got)
	}
}

func TestUnpublishRevokesTheLinkImmediately(t *testing.T) {
	st := publishableStore()
	s := newPublishServer(t, st, nil)

	pub := decode[model.Publication](t, doReq(t, s, http.MethodPost, "/api/articles/art-1/publish", model.PublishRequest{}, testToken))

	resp := doReq(t, s, http.MethodDelete, "/api/articles/art-1/publish", nil, testToken)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unpublish status = %d, want 204", resp.StatusCode)
	}
	if got := doReq(t, s, http.MethodGet, "/p/"+pub.Token, nil, "").StatusCode; got != http.StatusNotFound {
		t.Fatalf("revoked link status = %d, want 404", got)
	}
	if got := doReq(t, s, http.MethodDelete, "/api/articles/art-1/publish", nil, testToken).StatusCode; got != http.StatusNotFound {
		t.Fatalf("second unpublish status = %d, want 404", got)
	}
}

func TestGetPublicationReportsTheCurrentLink(t *testing.T) {
	st := publishableStore()
	s := newPublishServer(t, st, nil)

	if got := doReq(t, s, http.MethodGet, "/api/articles/art-1/publish", nil, testToken).StatusCode; got != http.StatusNotFound {
		t.Fatalf("status for an unpublished article = %d, want 404", got)
	}

	published := decode[model.Publication](t, doReq(t, s, http.MethodPost, "/api/articles/art-1/publish", model.PublishRequest{}, testToken))

	resp := doReq(t, s, http.MethodGet, "/api/articles/art-1/publish", nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := decode[model.Publication](t, resp)
	if got.Token != published.Token || got.URL != published.URL {
		t.Fatalf("GET returned %+v, want token/URL of %+v", got, published)
	}
}

func TestExpiredPublicationIsGone(t *testing.T) {
	st := publishableStore()
	s := newPublishServer(t, st, nil)

	pub := decode[model.Publication](t, doReq(t, s, http.MethodPost, "/api/articles/art-1/publish", model.PublishRequest{}, testToken))

	// Wind the TTL back past now, as an hour of wall-clock would.
	expired := st.pubs[pub.Token]
	expired.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	st.pubs[pub.Token] = expired

	resp := doReq(t, s, http.MethodGet, "/p/"+pub.Token, nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expired link status = %d, want 404", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "Page not found") {
		t.Fatalf("expired link body does not read as a 404 page:\n%s", body)
	}

	// The owner is told the same thing, so the UI offers to publish again.
	if got := doReq(t, s, http.MethodGet, "/api/articles/art-1/publish", nil, testToken).StatusCode; got != http.StatusNotFound {
		t.Fatalf("owner view of an expired publication = %d, want 404", got)
	}
}

func TestUnknownAndMalformedTokensAreNotFound(t *testing.T) {
	s := newPublishServer(t, publishableStore(), nil)

	for _, path := range []string{"/p/unknown-but-well-formed-token", "/p/short", "/p/..%2Fsecret"} {
		resp := doReq(t, s, http.MethodGet, path, nil, "")
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestPublishWithoutExpiryNeverExpires(t *testing.T) {
	st := publishableStore()
	st.settings.PublicPageTTLHours = 0
	s := newPublishServer(t, st, nil)

	pub := decode[model.Publication](t, doReq(t, s, http.MethodPost, "/api/articles/art-1/publish", model.PublishRequest{}, testToken))
	if !pub.ExpiresAt.IsZero() {
		t.Fatalf("ExpiresAt = %v, want zero for a TTL of 0", pub.ExpiresAt)
	}
	if got := doReq(t, s, http.MethodGet, "/p/"+pub.Token, nil, "").StatusCode; got != http.StatusOK {
		t.Fatalf("link status = %d, want 200", got)
	}
}

func TestDeletingAnArticleRemovesItsPage(t *testing.T) {
	st := publishableStore()
	s := newPublishServer(t, st, nil)

	pub := decode[model.Publication](t, doReq(t, s, http.MethodPost, "/api/articles/art-1/publish", model.PublishRequest{}, testToken))

	if got := doReq(t, s, http.MethodDelete, "/api/articles/art-1", nil, testToken).StatusCode; got != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", got)
	}
	if _, err := s.pub.Read(pub.Token); err == nil {
		t.Fatal("the page file outlived the article it was published from")
	}
}

func TestSweepPublicationsReclaimsExpiredPages(t *testing.T) {
	st := publishableStore()
	s := newPublishServer(t, st, nil)

	pub := decode[model.Publication](t, doReq(t, s, http.MethodPost, "/api/articles/art-1/publish", model.PublishRequest{}, testToken))

	expired := st.pubs[pub.Token]
	expired.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	st.pubs[pub.Token] = expired

	s.SweepPublications(t.Context())

	if _, ok := st.pubs[pub.Token]; ok {
		t.Fatal("expired publication survived the sweep")
	}
	if _, err := s.pub.Read(pub.Token); err == nil {
		t.Fatal("expired page file survived the sweep")
	}
}

func TestPublishingIsUnavailableWithoutAPageDirectory(t *testing.T) {
	// No PublicPagesDir: the publisher could not be built, so publishing must
	// say so rather than 500 — the rest of the API keeps working.
	s := newTestServerCfg(t, publishableStore(), &fakeIngestor{}, nil)

	resp := doReq(t, s, http.MethodPost, "/api/articles/art-1/publish", model.PublishRequest{}, testToken)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestPatchSettingsValidatesPublicPageTTL(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hours int
		want  int
	}{
		{"zero means no expiry", 0, http.StatusOK},
		{"in range", 24, http.StatusOK},
		{"negative", -1, http.StatusBadRequest},
		{"above a year", model.MaxPublicPageTTLHours + 1, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := publishableStore()
			st.updateSettings = func(p model.SettingsPatch) (model.Settings, error) {
				return model.Settings{PublicPageTTLHours: *p.PublicPageTTLHours}, nil
			}
			s := newPublishServer(t, st, nil)

			resp := doReq(t, s, http.MethodPatch, "/api/settings",
				map[string]int{"public_page_ttl_hours": tc.hours}, testToken)
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, tc.want, readBody(t, resp))
			}
		})
	}
}
