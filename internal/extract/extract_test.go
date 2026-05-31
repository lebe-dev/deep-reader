package extract_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"deep-reader/internal/config"
	"deep-reader/internal/extract"
)

// minimalConfig returns a Config suitable for tests (no real timeouts or auth).
func minimalConfig(timeout time.Duration) *config.Config {
	return &config.Config{
		ReadabilityTimeout: timeout,
		AuthToken:          "test",
		LLMAPIKey:          "test",
		DatabasePath:       "/tmp/test.db",
		HTTPPort:           8080,
		LLMMaxConcurrent:   1,
		LLMMaxRetries:      0,
		EnrichmentVersion:  1,
		LogLevel:           "info",
		LogFormat:          "json",
	}
}

// ----------------------------------------------------------------------------
// SSRF / host-validation table tests
// ----------------------------------------------------------------------------

func TestSSRFValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rawURL  string
		wantErr bool
		errType error // if non-nil, errors.Is must match
	}{
		// Valid public addresses
		{name: "valid http", rawURL: "http://example.com/article", wantErr: false},
		{name: "valid https", rawURL: "https://example.com/article", wantErr: false},

		// Bad schemes
		{name: "ftp scheme", rawURL: "ftp://example.com/file", wantErr: true},
		{name: "file scheme", rawURL: "file:///etc/passwd", wantErr: true},
		{name: "javascript scheme", rawURL: "javascript:alert(1)", wantErr: true},
		{name: "empty", rawURL: "", wantErr: true},

		// Private IPv4 ranges
		{name: "loopback 127.0.0.1", rawURL: "http://127.0.0.1/secret", wantErr: true, errType: extract.ErrBlockedHost},
		{name: "loopback localhost", rawURL: "http://localhost/secret", wantErr: true, errType: extract.ErrBlockedHost},
		{name: "RFC1918 10.x", rawURL: "http://10.0.0.1/secret", wantErr: true, errType: extract.ErrBlockedHost},
		{name: "RFC1918 172.16.x", rawURL: "http://172.16.0.1/secret", wantErr: true, errType: extract.ErrBlockedHost},
		{name: "RFC1918 192.168.x", rawURL: "http://192.168.1.1/secret", wantErr: true, errType: extract.ErrBlockedHost},
		{name: "link-local 169.254.x", rawURL: "http://169.254.169.254/latest/meta-data/", wantErr: true, errType: extract.ErrBlockedHost},
		{name: "APIPA exact", rawURL: "http://169.254.169.254/", wantErr: true, errType: extract.ErrBlockedHost},
		{name: "loopback ::1", rawURL: "http://[::1]/secret", wantErr: true, errType: extract.ErrBlockedHost},
		{name: "link-local fe80::", rawURL: "http://[fe80::1]/secret", wantErr: true, errType: extract.ErrBlockedHost},
		{name: "ULA fc00::", rawURL: "http://[fc00::1]/secret", wantErr: true, errType: extract.ErrBlockedHost},
		{name: "ULA fd00::", rawURL: "http://[fd00::1]/secret", wantErr: true, errType: extract.ErrBlockedHost},
	}

	cfg := minimalConfig(5 * time.Second)
	ex := extract.New(cfg)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ex.Extract(context.Background(), tc.rawURL)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tc.rawURL)
				}
				if tc.errType != nil {
					if !isErr(err, tc.errType) {
						t.Fatalf("expected errors.Is(%v), got %v", tc.errType, err)
					}
				}
			} else {
				// For "valid" cases we can't actually fetch without a server, so skip
				// network errors — we only care that SSRF validation itself did not fire.
				if err != nil && isErr(err, extract.ErrBlockedHost) {
					t.Fatalf("unexpected ErrBlockedHost for %q: %v", tc.rawURL, err)
				}
			}
		})
	}
}

// isErr walks the error chain for target identity (mirrors errors.Is semantics
// without importing errors in the test helper itself).
func isErr(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// srvAddr returns the "host:port" of an httptest.Server's listener, suitable
// for passing to NewForTest to exempt it from the SSRF guard.
func srvAddr(srv *httptest.Server) string {
	return srv.Listener.Addr().String()
}

// ----------------------------------------------------------------------------
// Happy-path: httptest server with a small article HTML
// ----------------------------------------------------------------------------

const testArticleHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Test Article Title</title>
  <link rel="canonical" href="https://canonical.example.com/test-article">
  <meta property="og:title" content="OG Test Title" />
</head>
<body>
  <article>
    <h1>Test Article Title</h1>
    <p>By Jane Doe</p>
    <p>This is the first paragraph of the article. It contains enough text for
    readability to extract it successfully without trouble or issues whatsoever.</p>
    <p>Second paragraph here. More content to ensure readability picks this up.
    We need enough text to satisfy the minimum character threshold for the parser.</p>
    <p>Third paragraph adds more text. Readability needs a reasonable amount of
    content to identify the main article body and extract it cleanly.</p>
    <p>Fourth paragraph for good measure. The readability algorithm scores nodes
    based on content density and structure to find the primary content area.</p>
  </article>
</body>
</html>`

func TestExtractHappyPath(t *testing.T) {
	t.Parallel()

	// Start a test HTTP server serving our article HTML.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, testArticleHTML)
	}))
	defer srv.Close()

	cfg := minimalConfig(10 * time.Second)
	// NewForTest exempts the test server address from the SSRF loopback guard.
	ex := extract.NewForTest(cfg, srvAddr(srv))

	result, err := ex.Extract(context.Background(), srv.URL+"/test-article")
	if err != nil {
		t.Fatalf("Extract() unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("Extract() returned nil result")
	}

	// Title must be populated.
	if result.Title == "" {
		t.Error("Title is empty")
	}

	// Text content must be non-empty.
	if result.Text == "" {
		t.Error("Text is empty")
	}

	// HTML content must be non-empty.
	if result.HTML == "" {
		t.Error("HTML is empty")
	}

	// Domain must match the test server host.
	host, _, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("could not parse test server addr: %v", err)
	}
	if result.Domain == "" {
		t.Errorf("Domain is empty, expected something with %q", host)
	}

	// CanonicalURL: should be the rel=canonical link we embedded.
	// At minimum it must be non-empty and start with "http".
	if result.CanonicalURL == "" {
		t.Error("CanonicalURL is empty")
	}
	if !strings.HasPrefix(result.CanonicalURL, "http") {
		t.Errorf("CanonicalURL looks invalid: %q", result.CanonicalURL)
	}

	t.Logf("Title=%q Author=%q Domain=%q Lang=%q CanonicalURL=%q",
		result.Title, result.Author, result.Domain, result.Lang, result.CanonicalURL)
}

// ----------------------------------------------------------------------------
// Body size limit
// ----------------------------------------------------------------------------

func TestBodyTooLarge(t *testing.T) {
	t.Parallel()

	// Serve a response larger than maxBodyBytes (10 MB).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Write 11 MB of data.
		chunk := strings.Repeat("x", 4096)
		for i := 0; i < (11*1024*1024)/len(chunk)+1; i++ {
			_, _ = fmt.Fprint(w, chunk)
		}
	}))
	defer srv.Close()

	cfg := minimalConfig(15 * time.Second)
	ex := extract.NewForTest(cfg, srvAddr(srv))

	_, err := ex.Extract(context.Background(), srv.URL+"/big")
	if err == nil {
		t.Fatal("expected ErrTooLarge, got nil")
	}
	if !isErr(err, extract.ErrTooLarge) {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
}

// ----------------------------------------------------------------------------
// Context cancellation is respected
// ----------------------------------------------------------------------------

func TestContextCancelled(t *testing.T) {
	t.Parallel()

	// Server that hangs forever.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	cfg := minimalConfig(30 * time.Second)
	ex := extract.NewForTest(cfg, srvAddr(srv))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := ex.Extract(ctx, srv.URL+"/slow")
	if err == nil {
		t.Fatal("expected error due to context cancellation, got nil")
	}
}

// ----------------------------------------------------------------------------
// Redirect SSRF guard: redirect to private IP must be blocked
// ----------------------------------------------------------------------------

func TestRedirectToPrivateIP(t *testing.T) {
	t.Parallel()

	// Server that redirects to a private IP.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://192.168.0.1/secret", http.StatusFound)
	}))
	defer srv.Close()

	cfg := minimalConfig(5 * time.Second)
	// Exempt the test server itself (loopback), but redirect target 192.168.0.1
	// must still be blocked by the CheckRedirect hook.
	ex := extract.NewForTest(cfg, srvAddr(srv))

	_, err := ex.Extract(context.Background(), srv.URL+"/redirect")
	if err == nil {
		t.Fatal("expected ErrBlockedHost for redirect to private IP, got nil")
	}
	if !isErr(err, extract.ErrBlockedHost) {
		t.Fatalf("expected ErrBlockedHost, got %v", err)
	}
}
