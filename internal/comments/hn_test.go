package comments

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"deep-reader/internal/model"
	"deep-reader/internal/ports"
)

// loadThreadFixture decodes the recorded Algolia response used across the tests.
func loadThreadFixture(t *testing.T) *hnItem {
	t.Helper()
	raw, err := os.ReadFile("testdata/thread.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var item hnItem
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return &item
}

// newTestHN points the source at a test server instead of the public API.
func newTestHN(baseURL string) *hackerNews {
	return &hackerNews{client: &http.Client{Timeout: 5 * time.Second}, baseURL: baseURL}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func TestHackerNewsMatch(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{name: "item permalink", url: "https://news.ycombinator.com/item?id=49505014", want: true},
		{name: "www host", url: "https://www.news.ycombinator.com/item?id=1", want: true},
		{name: "http scheme", url: "http://news.ycombinator.com/item?id=1", want: true},
		{name: "extra query parameters", url: "https://news.ycombinator.com/item?id=1&p=2", want: true},
		{name: "front page", url: "https://news.ycombinator.com/", want: false},
		{name: "user profile", url: "https://news.ycombinator.com/user?id=pg", want: false},
		{name: "non-numeric id", url: "https://news.ycombinator.com/item?id=pg", want: false},
		{name: "missing id", url: "https://news.ycombinator.com/item", want: false},
		{name: "another site", url: "https://example.com/item?id=1", want: false},
		{name: "lookalike host", url: "https://news.ycombinator.com.evil.test/item?id=1", want: false},
	}

	hn := newTestHN("http://unused.test")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hn.Match(mustParse(t, tc.url)); got != tc.want {
				t.Fatalf("Match(%q): got %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

func TestHackerNewsFetch(t *testing.T) {
	fixture, err := os.ReadFile("testdata/thread.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	hn := newTestHN(srv.URL)
	result, err := hn.Fetch(context.Background(), mustParse(t, "https://news.ycombinator.com/item?id=49505014"))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if gotPath != "/items/49505014" {
		t.Errorf("requested path: got %q, want %q", gotPath, "/items/49505014")
	}
	if result.SourceType != model.SourceTypeComments {
		t.Errorf("source type: got %q, want %q", result.SourceType, model.SourceTypeComments)
	}
	if result.ContentFormat != model.ContentFormatMarkdown {
		t.Errorf("content format: got %q, want %q", result.ContentFormat, model.ContentFormatMarkdown)
	}
	if result.Title != "Why do I lose my passion?" {
		t.Errorf("title: got %q", result.Title)
	}
	if result.Author != "hanstyle" {
		t.Errorf("author: got %q, want the submitter", result.Author)
	}
	if result.Domain != hnHost {
		t.Errorf("domain: got %q, want %q", result.Domain, hnHost)
	}
	if result.CanonicalURL != "https://news.ycombinator.com/item?id=49505014" {
		t.Errorf("canonical URL: got %q", result.CanonicalURL)
	}
	if !strings.Contains(result.Text, "## alice") {
		t.Errorf("rendered thread missing its comments:\n%s", result.Text)
	}
}

func TestHackerNewsFetchErrors(t *testing.T) {
	cases := []struct {
		name          string
		status        int
		body          string
		wantRetryable bool
		wantErr       error
	}{
		{
			name:          "not indexed yet is retried",
			status:        http.StatusNotFound,
			body:          `{"error":"not found"}`,
			wantRetryable: true,
			wantErr:       ErrUnknownItem,
		},
		{
			name:          "rate limiting is retried",
			status:        http.StatusTooManyRequests,
			body:          "slow down",
			wantRetryable: true,
			wantErr:       ErrUnknownItem,
		},
		{
			name:          "server error is retried",
			status:        http.StatusBadGateway,
			body:          "bad gateway",
			wantRetryable: true,
			wantErr:       ErrUnknownItem,
		},
		{
			name:          "a client error is terminal",
			status:        http.StatusBadRequest,
			body:          "bad request",
			wantRetryable: false,
		},
		{
			name:          "an empty thread is terminal",
			status:        http.StatusOK,
			body:          `{"id":1,"type":"story","author":"pg","title":"No replies","children":[]}`,
			wantRetryable: false,
			wantErr:       ErrEmptyThread,
		},
		{
			name:          "undecodable body is terminal",
			status:        http.StatusOK,
			body:          "not json",
			wantRetryable: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			hn := newTestHN(srv.URL)
			_, err := hn.Fetch(context.Background(), mustParse(t, "https://news.ycombinator.com/item?id=1"))
			if err == nil {
				t.Fatal("expected an error")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("error: got %v, want it to wrap %v", err, tc.wantErr)
			}

			var temp *TemporaryError
			if got := errors.As(err, &temp); got != tc.wantRetryable {
				t.Fatalf("retryable: got %v, want %v (err: %v)", got, tc.wantRetryable, err)
			}
		})
	}
}

func TestHackerNewsFetchTransportFailureIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // nothing is listening any more

	hn := newTestHN(srv.URL)
	_, err := hn.Fetch(context.Background(), mustParse(t, "https://news.ycombinator.com/item?id=1"))
	if err == nil {
		t.Fatal("expected an error")
	}
	var temp *TemporaryError
	if !errors.As(err, &temp) {
		t.Fatalf("a transport failure must be retryable, got %v", err)
	}
}

// fakeExtractor records whether the ordinary article path was taken.
type fakeExtractor struct {
	calls int
	url   string
}

func (f *fakeExtractor) Extract(_ context.Context, rawURL string) (*ports.ExtractResult, error) {
	f.calls++
	f.url = rawURL
	return &ports.ExtractResult{CanonicalURL: rawURL, Text: "article prose"}, nil
}

func TestRouter(t *testing.T) {
	fixture, err := os.ReadFile("testdata/thread.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	t.Run("a thread URL goes to the comment source", func(t *testing.T) {
		fallback := &fakeExtractor{}
		router := NewRouter(fallback, newTestHN(srv.URL))

		result, err := router.Extract(context.Background(), "https://news.ycombinator.com/item?id=49505014")
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if fallback.calls != 0 {
			t.Errorf("the article extractor must not run for a thread URL")
		}
		if result.SourceType != model.SourceTypeComments {
			t.Errorf("source type: got %q, want %q", result.SourceType, model.SourceTypeComments)
		}
	})

	t.Run("any other URL goes to the article extractor", func(t *testing.T) {
		fallback := &fakeExtractor{}
		router := NewRouter(fallback, newTestHN(srv.URL))

		result, err := router.Extract(context.Background(), "https://example.com/post")
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if fallback.calls != 1 || fallback.url != "https://example.com/post" {
			t.Errorf("the article extractor should have been called once with the URL, got %d call(s) for %q", fallback.calls, fallback.url)
		}
		if result.SourceType != "" {
			t.Errorf("an article result carries no comment source type, got %q", result.SourceType)
		}
	})

	t.Run("a malformed URL goes to the article extractor for validation", func(t *testing.T) {
		fallback := &fakeExtractor{}
		router := NewRouter(fallback, newTestHN(srv.URL))

		if _, err := router.Extract(context.Background(), "://nonsense"); err != nil {
			t.Fatalf("extract: %v", err)
		}
		if fallback.calls != 1 {
			t.Errorf("the article extractor owns URL validation, got %d call(s)", fallback.calls)
		}
	})

	t.Run("a source failure is not retried as an article", func(t *testing.T) {
		down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		defer down.Close()

		fallback := &fakeExtractor{}
		router := NewRouter(fallback, newTestHN(down.URL))

		_, err := router.Extract(context.Background(), "https://news.ycombinator.com/item?id=1")
		if err == nil {
			t.Fatal("expected an error")
		}
		if fallback.calls != 0 {
			t.Errorf("scraping the thread page as an article would store navigation chrome, got %d call(s)", fallback.calls)
		}
		var temp *TemporaryError
		if !errors.As(err, &temp) {
			t.Errorf("the wrapped error must stay retryable, got %v", err)
		}
	})
}
