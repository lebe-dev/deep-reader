// Package extract implements URL fetching and article content extraction with
// SSRF protection. It satisfies the ports.Extractor interface.
//
// # SSRF protection
//
// Before making any HTTP request (including redirect targets), the resolved IP
// is checked against:
//   - loopback (127.0.0.0/8, ::1)
//   - RFC 1918 private ranges (10/8, 172.16/12, 192.168/16)
//   - link-local (169.254.0.0/16, fe80::/10)
//   - ULA IPv6 (fc00::/7)
//   - the AWS/GCP metadata endpoint 169.254.169.254
//
// Any match returns ErrBlockedHost before a connection is opened.
package extract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	nurl "net/url"
	"strings"
	"time"

	readability "github.com/go-shiori/go-readability"
	"golang.org/x/net/html"

	"deep-reader/internal/config"
	"deep-reader/internal/ports"
)

// Sentinel errors returned by Extract. Callers should match them with
// errors.Is.
var (
	// ErrBlockedHost is returned when the resolved host falls in a private /
	// loopback / link-local / ULA range (SSRF guard).
	ErrBlockedHost = errors.New("blocked host: private or reserved IP")
	// ErrTooLarge is returned when the response body exceeds maxBodyBytes.
	ErrTooLarge = errors.New("response body too large")
	// ErrUnparseable is returned when go-readability cannot extract article
	// content (e.g. the page has insufficient text).
	ErrUnparseable = errors.New("article content could not be extracted")
)

// maxBodyBytes caps the HTTP response body read to 10 MiB.
const maxBodyBytes = 10 * 1024 * 1024

// userAgent is sent in all outbound requests. Realistic enough to avoid
// trivial bot-blocking while being honest about automation.
const userAgent = "Mozilla/5.0 (compatible; DeepReader/1.0; +https://github.com/deep-reader)"

// Extractor fetches a URL and extracts readable article content.
// Construct via New(cfg).
type Extractor struct {
	client      *http.Client
	timeout     time.Duration
	allowedAddr string // if non-empty, this specific host:port bypasses the SSRF guard (tests only)
}

// New creates an Extractor configured from cfg. The HTTP client uses a custom
// dialer and CheckRedirect hook to enforce SSRF guards on every connection,
// including redirect targets.
func New(cfg *config.Config) *Extractor {
	return newWithOptions(cfg, "")
}

// newWithOptions is the internal constructor. allowedAddr, when non-empty,
// exempts that specific "host:port" from the SSRF guard. Used only by tests
// (httptest.Server binds to 127.0.0.1 with a random port).
func newWithOptions(cfg *config.Config, allowedAddr string) *Extractor {
	timeout := cfg.ReadabilityTimeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}

	guard := func(host string) error {
		return checkHost(host)
	}

	// Custom dialer: reject private/reserved IPs at the TCP dial stage so that
	// even requests that bypass CheckRedirect (direct IPs in URLs) are caught.
	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("extract: invalid addr %q: %w", addr, err)
			}
			// allowedAddr exempts a specific host:port (test server) from SSRF checks.
			if allowedAddr == "" || net.JoinHostPort(host, port) != allowedAddr {
				if err := guard(host); err != nil {
					return nil, err
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
		},
	}

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			// Redirect targets are always checked; the allowedAddr exemption only
			// applies at the dialer level for the originating request.
			return checkHost(req.URL.Hostname())
		},
	}

	return &Extractor{
		client:      client,
		timeout:     timeout,
		allowedAddr: allowedAddr,
	}
}

// Extract validates rawURL, fetches it with SSRF protection, limits the body
// to maxBodyBytes, and runs go-readability to extract article content.
//
// The returned CanonicalURL is derived from rel=canonical or og:url meta tags
// in the page; if neither is found it falls back to the final (post-redirect)
// request URL.
//
// It implements ports.Extractor.
func (e *Extractor) Extract(ctx context.Context, rawURL string) (*ports.ExtractResult, error) {
	// Validate scheme first (no network traffic yet).
	if err := validateURL(rawURL); err != nil {
		return nil, err
	}

	// Pre-flight: resolve the host and check for private ranges.
	parsed, err := nurl.ParseRequestURI(rawURL)
	if err != nil {
		return nil, fmt.Errorf("extract: invalid URL %q: %w", rawURL, err)
	}
	// Skip SSRF pre-flight only for the explicitly allowed test address.
	hostport := net.JoinHostPort(parsed.Hostname(), parsed.Port())
	if e.allowedAddr == "" || hostport != e.allowedAddr {
		if err := checkHost(parsed.Hostname()); err != nil {
			slog.Warn("extract: SSRF guard blocked host", "url", rawURL, "host", parsed.Hostname(), "err", err)
			return nil, err
		}
	}

	// Build and execute the HTTP request.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("extract: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	slog.Debug("extract: fetching URL", "url", rawURL, "timeout", e.timeout)
	start := time.Now()
	resp, err := e.client.Do(req)
	if err != nil {
		// Wrap ErrBlockedHost if it comes back through the transport.
		if isBlockedErr(err) {
			slog.Warn("extract: SSRF guard blocked host during fetch", "url", rawURL, "err", err)
			return nil, ErrBlockedHost
		}
		slog.Warn("extract: fetch failed", "url", rawURL, "err", err)
		return nil, fmt.Errorf("extract: fetch %q: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	slog.Debug("extract: response received",
		"url", rawURL,
		"status", resp.StatusCode,
		"content_type", resp.Header.Get("Content-Type"),
		"duration_ms", time.Since(start).Milliseconds(),
	)

	// Enforce body size limit.
	limited := io.LimitReader(resp.Body, maxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("extract: read body: %w", err)
	}
	if int64(len(body)) > maxBodyBytes {
		slog.Warn("extract: response body exceeds limit", "url", rawURL, "limit_bytes", maxBodyBytes)
		return nil, ErrTooLarge
	}

	// The final URL after redirects.
	finalURL := resp.Request.URL
	if finalURL.String() != rawURL {
		slog.Debug("extract: followed redirect", "from", rawURL, "to", finalURL.String())
	}

	// Parse HTML once to extract both the canonical URL and the article.
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: HTML parse failed: %v", ErrUnparseable, err)
	}

	canonicalURL := extractCanonical(doc, finalURL)

	// Run readability on the already-parsed document.
	parser := readability.NewParser()
	article, err := parser.ParseDocument(doc, finalURL)
	if err != nil {
		slog.Warn("extract: readability parse failed", "url", rawURL, "err", err)
		return nil, fmt.Errorf("%w: %v", ErrUnparseable, err)
	}

	if article.Content == "" && article.TextContent == "" {
		slog.Warn("extract: readability returned empty content", "url", rawURL)
		return nil, fmt.Errorf("%w: readability returned empty content", ErrUnparseable)
	}

	slog.Debug("extract: article parsed",
		"url", rawURL,
		"canonical_url", canonicalURL,
		"title", article.Title,
		"body_bytes", len(body),
		"text_bytes", len(article.TextContent),
		"lang", article.Language,
	)

	result := &ports.ExtractResult{
		CanonicalURL: canonicalURL,
		Title:        article.Title,
		Author:       article.Byline,
		Domain:       finalURL.Hostname(),
		Lang:         article.Language,
		HTML:         article.Content,
		Text:         article.TextContent,
	}
	return result, nil
}

// validateURL checks that rawURL is non-empty and uses http or https.
func validateURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("extract: empty URL")
	}
	u, err := nurl.ParseRequestURI(rawURL)
	if err != nil {
		return fmt.Errorf("extract: invalid URL %q: %w", rawURL, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return nil
	default:
		return fmt.Errorf("extract: unsupported scheme %q (only http/https allowed)", u.Scheme)
	}
}

// checkHost resolves hostname to IPs and rejects any address that falls in a
// private, loopback, link-local, or ULA range. A bare IPv4/IPv6 literal is
// checked directly.
func checkHost(hostname string) error {
	// Strip brackets from IPv6 literals (e.g. [::1] → ::1).
	hostname = strings.TrimPrefix(hostname, "[")
	hostname = strings.TrimSuffix(hostname, "]")

	if hostname == "" {
		return fmt.Errorf("%w: empty hostname", ErrBlockedHost)
	}

	// If it's already an IP literal, check it directly without a DNS lookup.
	if ip := net.ParseIP(hostname); ip != nil {
		return checkIP(ip)
	}

	// Resolve the hostname to IPs. All resolved addresses must pass.
	addrs, err := net.LookupHost(hostname)
	if err != nil {
		// Resolution failure is not an SSRF block; let the HTTP client handle it.
		return nil
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		if err := checkIP(ip); err != nil {
			return err
		}
	}
	return nil
}

// checkIP returns ErrBlockedHost if ip is in any private/reserved range.
func checkIP(ip net.IP) error {
	if ip.IsLoopback() {
		return fmt.Errorf("%w: loopback address %s", ErrBlockedHost, ip)
	}
	if ip.IsPrivate() {
		return fmt.Errorf("%w: private address %s", ErrBlockedHost, ip)
	}
	if ip.IsLinkLocalUnicast() {
		return fmt.Errorf("%w: link-local address %s", ErrBlockedHost, ip)
	}
	if ip.IsLinkLocalMulticast() {
		return fmt.Errorf("%w: link-local multicast address %s", ErrBlockedHost, ip)
	}
	// ULA fc00::/7 is not covered by IsPrivate in all Go versions; check explicitly.
	if isULA(ip) {
		return fmt.Errorf("%w: ULA IPv6 address %s", ErrBlockedHost, ip)
	}
	// Belt-and-suspenders: 169.254.169.254 is covered by IsLinkLocalUnicast above,
	// but make it explicit for the AWS/GCP metadata endpoint.
	if ip.Equal(net.ParseIP("169.254.169.254")) {
		return fmt.Errorf("%w: cloud metadata endpoint %s", ErrBlockedHost, ip)
	}
	return nil
}

// isULA returns true if ip is in the IPv6 ULA range fc00::/7.
func isULA(ip net.IP) bool {
	ip6 := ip.To16()
	if ip6 == nil {
		return false
	}
	// IPv4 addresses mapped to IPv6 are not ULA.
	if ip.To4() != nil {
		return false
	}
	// fc00::/7 means first byte is 0xfc or 0xfd.
	return ip6[0]&0xfe == 0xfc
}

// isBlockedErr returns true if err's chain contains ErrBlockedHost.
func isBlockedErr(err error) bool {
	return errors.Is(err, ErrBlockedHost)
}

// extractCanonical searches the parsed HTML document for a rel=canonical link
// or og:url meta tag. It returns the absolute URL resolved against base, or
// base.String() if neither hint is found.
func extractCanonical(doc *html.Node, base *nurl.URL) string {
	var canonical string

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if canonical != "" {
			return
		}
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "link":
				rel, href := attrVal(n, "rel"), attrVal(n, "href")
				if strings.EqualFold(rel, "canonical") && href != "" {
					canonical = resolveURL(href, base)
					return
				}
			case "meta":
				prop := attrVal(n, "property")
				if strings.EqualFold(prop, "og:url") {
					if content := attrVal(n, "content"); content != "" {
						canonical = resolveURL(content, base)
						return
					}
				}
			case "body":
				// Stop descending into body — canonical hints are only in <head>.
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if canonical == "" {
		return base.String()
	}
	return canonical
}

// resolveURL resolves href against base, returning base.String() on failure.
func resolveURL(href string, base *nurl.URL) string {
	ref, err := nurl.Parse(href)
	if err != nil {
		return base.String()
	}
	return base.ResolveReference(ref).String()
}

// attrVal returns the value of the named attribute on n, or "".
func attrVal(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, name) {
			return a.Val
		}
	}
	return ""
}
