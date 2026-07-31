package publish

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// tokenBytes is the entropy behind a share token: 128 bits, rendered as 22
// base64url characters. The token is the only thing protecting a published
// page, so it has to be far out of brute-force reach — but a public reader also
// has to be able to paste it, so this stops well short of a 512-bit URL.
const tokenBytes = 16

// tokenPattern is what a well-formed token looks like. Every token arriving
// from a request is matched against it before it is used to build a file path,
// so no request can address anything outside the publication directory.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

// ErrNotFound is returned when a token has no page file on disk.
var ErrNotFound = errors.New("published page not found")

// NewToken returns a fresh, unguessable share token.
func NewToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating publication token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ValidToken reports whether s is shaped like a token this package issued.
func ValidToken(s string) bool { return tokenPattern.MatchString(s) }

// Publisher stores rendered public pages as files under a single directory,
// one <token>.html per publication.
//
// Keeping the pages on disk — rather than in the database — is what lets the
// HTTP layer answer a public request with a plain file read: no template
// execution, no article query, no enrichment decode per view.
type Publisher struct {
	dir string
}

// NewPublisher returns a Publisher writing into dir, creating it if needed.
func NewPublisher(dir string) (*Publisher, error) {
	if dir == "" {
		return nil, errors.New("publication directory is not configured")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("creating publication directory %s: %w", dir, err)
	}
	return &Publisher{dir: dir}, nil
}

// Dir returns the directory the pages are stored in.
func (p *Publisher) Dir() string { return p.dir }

// Write stores (or replaces) the page for token.
func (p *Publisher) Write(token string, html []byte) error {
	path, err := p.path(token)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, html, 0o640); err != nil {
		return fmt.Errorf("writing public page: %w", err)
	}
	return nil
}

// Read returns the stored page for token, or ErrNotFound.
func (p *Publisher) Read(token string) ([]byte, error) {
	path, err := p.path(token)
	if err != nil {
		return nil, err
	}
	html, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading public page: %w", err)
	}
	return html, nil
}

// Remove deletes the page for token. Removing a page that is not there is not
// an error: unpublishing must succeed even if the file was already swept.
func (p *Publisher) Remove(token string) error {
	path, err := p.path(token)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing public page: %w", err)
	}
	return nil
}

// RemoveOrphans deletes every stored page whose token is not in live, and
// reports how many files it removed.
//
// It is the backstop for the two ways a file outlives its record: an article
// deleted from the library (the publication row goes with it, via ON DELETE
// CASCADE) and a publication whose TTL has run out and been pruned.
func (p *Publisher) RemoveOrphans(live []string) (int, error) {
	keep := make(map[string]struct{}, len(live))
	for _, token := range live {
		keep[token] = struct{}{}
	}

	entries, err := os.ReadDir(p.dir)
	if err != nil {
		return 0, fmt.Errorf("listing publication directory: %w", err)
	}

	removed := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		token := strings.TrimSuffix(e.Name(), ".html")
		if _, ok := keep[token]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(p.dir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return removed, fmt.Errorf("removing orphaned public page: %w", err)
		}
		removed++
	}
	return removed, nil
}

// path resolves the file backing a token, rejecting anything that is not a
// well-formed token so a request can never escape the directory.
func (p *Publisher) path(token string) (string, error) {
	if !ValidToken(token) {
		return "", ErrNotFound
	}
	return filepath.Join(p.dir, token+".html"), nil
}
