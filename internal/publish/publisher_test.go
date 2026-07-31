package publish

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestPublisher(t *testing.T) *Publisher {
	t.Helper()
	p, err := NewPublisher(t.TempDir())
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	return p
}

func TestNewTokenIsUnguessableAndUnique(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for range 100 {
		token, err := NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if !ValidToken(token) {
			t.Fatalf("NewToken returned %q, which ValidToken rejects", token)
		}
		if len(token) < 22 {
			t.Fatalf("token %q is %d chars, want at least 22 (128 bits)", token, len(token))
		}
		if _, dup := seen[token]; dup {
			t.Fatalf("NewToken repeated %q", token)
		}
		seen[token] = struct{}{}
	}
}

func TestValidTokenRejectsPathTraversal(t *testing.T) {
	for _, bad := range []string{"", "..", "../../etc/passwd", "a/b", "tok.en", "short", strings.Repeat("a", 65)} {
		if ValidToken(bad) {
			t.Errorf("ValidToken(%q) = true, want false", bad)
		}
	}
}

func TestWriteReadRemoveRoundTrip(t *testing.T) {
	p := newTestPublisher(t)
	token, _ := NewToken()

	if err := p.Write(token, []byte("<html>page</html>")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := p.Read(token)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got) != "<html>page</html>" {
		t.Fatalf("Read returned %q", got)
	}

	if err := p.Remove(token); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := p.Read(token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Read after Remove = %v, want ErrNotFound", err)
	}
	// Unpublishing twice must not fail — the sweeper may have got there first.
	if err := p.Remove(token); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
}

func TestReadRejectsTraversalToken(t *testing.T) {
	p := newTestPublisher(t)
	outside := filepath.Join(filepath.Dir(p.Dir()), "secret.html")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatalf("seeding file outside the directory: %v", err)
	}

	if _, err := p.Read("../secret"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Read of a traversal token = %v, want ErrNotFound", err)
	}
}

func TestRemoveOrphansKeepsLivePagesOnly(t *testing.T) {
	p := newTestPublisher(t)
	live, _ := NewToken()
	dead, _ := NewToken()

	for _, token := range []string{live, dead} {
		if err := p.Write(token, []byte("page")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	// A non-page file in the directory must be left alone.
	if err := os.WriteFile(filepath.Join(p.Dir(), "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seeding unrelated file: %v", err)
	}

	removed, err := p.RemoveOrphans([]string{live})
	if err != nil {
		t.Fatalf("RemoveOrphans: %v", err)
	}
	if removed != 1 {
		t.Fatalf("RemoveOrphans removed %d files, want 1", removed)
	}
	if _, err := p.Read(live); err != nil {
		t.Fatalf("live page was swept: %v", err)
	}
	if _, err := p.Read(dead); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphan page survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir(), "notes.txt")); err != nil {
		t.Fatalf("unrelated file was removed: %v", err)
	}
}

func TestRenderPageEmbedsOpenGraphMetadata(t *testing.T) {
	html, err := RenderPage(Page{
		Title:        "Заголовок",
		Description:  "Краткое описание",
		URL:          "https://reader.example/p/abc",
		Author:       "Jane Doe",
		SourceURL:    "https://example.com/post",
		SourceDomain: "example.com",
		Lang:         "ru",
		PublishedAt:  time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC),
		Blocks: []Block{{Segments: []Segment{
			{Text: "Переведённое предложение.", Translated: true},
			{Text: "Untranslated tail.", Translated: false},
		}}},
	})
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}

	page := string(html)
	for _, want := range []string{
		`<html lang="ru">`,
		`<title>Заголовок</title>`,
		`<meta property="og:title" content="Заголовок">`,
		`<meta property="og:description" content="Краткое описание">`,
		`<meta property="og:url" content="https://reader.example/p/abc">`,
		`<meta property="og:type" content="article">`,
		`<meta name="twitter:card" content="summary">`,
		`content="2026-07-31T10:00:00Z"`,
		"Переведённое предложение.",
		`<span class="orig" lang="en">Untranslated tail.</span>`,
		"example.com",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("rendered page is missing %q", want)
		}
	}
}

func TestRenderPageEscapesUserSuppliedMetadata(t *testing.T) {
	html, err := RenderPage(Page{
		Title:       `Break"><script>alert(1)</script>`,
		Description: `desc"><img src=x onerror=alert(1)>`,
		Blocks: []Block{{Segments: []Segment{
			{Text: "<script>alert(2)</script>", Translated: true},
		}}},
	})
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}

	page := string(html)
	if strings.Contains(page, "<script>alert(1)</script>") || strings.Contains(page, "<script>alert(2)</script>") {
		t.Fatalf("user-supplied markup was not escaped:\n%s", page)
	}
	// The payload text may survive inside an attribute value — what must not
	// survive is the quote that would end the attribute and start a tag.
	if strings.Contains(page, `"><img`) {
		t.Fatalf("attribute injection was not escaped:\n%s", page)
	}
	if !strings.Contains(page, `&#34;&gt;&lt;img`) {
		t.Fatalf("description payload was not escaped as expected:\n%s", page)
	}
}

func TestRenderPageDefaultsLanguages(t *testing.T) {
	html, err := RenderPage(Page{Title: "T"})
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if !strings.Contains(string(html), `<html lang="ru">`) {
		t.Fatalf("empty Lang did not default to ru:\n%s", html)
	}
}
