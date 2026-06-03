package store_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"deep-reader/internal/config"
	"deep-reader/internal/model"
	"deep-reader/internal/ports"
	"deep-reader/internal/store"
)

// openStore creates a fresh SQLite store in a temp directory.
func openStore(t *testing.T) *store.SQLite {
	t.Helper()
	cfg := &config.Config{
		DatabasePath: filepath.Join(t.TempDir(), "test.db"),
	}
	s, err := store.NewSQLite(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// makeArticle builds a minimal valid Article with the given url.
func makeArticle(url string) *model.Article {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(url)))
	return &model.Article{
		ID:                store.NewID(),
		SourceURL:         url,
		URLHash:           hash,
		Title:             "Test Article",
		Author:            "Author",
		SourceDomain:      "example.com",
		Lang:              "en",
		OriginalText:      "Hello world",
		Tokens:            []model.Token{{Index: 0, Text: "Hello", Start: 0, End: 5}},
		Status:            model.StatusFetched,
		EnrichmentVersion: 1,
		CreatedAt:         time.Now().UTC().Truncate(time.Second),
		UpdatedAt:         time.Now().UTC().Truncate(time.Second),
	}
}

// ── Settings ──────────────────────────────────────────────────────────────────

func TestGetSettings_Defaults(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	got, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.CEFRLevel != model.CEFRA2 {
		t.Errorf("CEFRLevel: got %q, want %q", got.CEFRLevel, model.CEFRA2)
	}
	if got.TargetLanguage != model.DefaultTargetLanguage {
		t.Errorf("TargetLanguage: got %q, want %q", got.TargetLanguage, model.DefaultTargetLanguage)
	}
	if got.MinDifficultyToHighlight != model.CEFRB1 {
		t.Errorf("MinDifficultyToHighlight: got %q, want %q", got.MinDifficultyToHighlight, model.CEFRB1)
	}
	if got.MarkdownWarnThreshold != model.DefaultMarkdownWarnThreshold {
		t.Errorf("MarkdownWarnThreshold: got %d, want %d", got.MarkdownWarnThreshold, model.DefaultMarkdownWarnThreshold)
	}
}

func TestUpdateSettings(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	newLevel := model.CEFRB2
	got, err := s.UpdateSettings(ctx, model.SettingsPatch{CEFRLevel: &newLevel})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if got.CEFRLevel != newLevel {
		t.Errorf("CEFRLevel after update: got %q, want %q", got.CEFRLevel, newLevel)
	}
	// Other fields should remain unchanged.
	if got.TargetLanguage != model.DefaultTargetLanguage {
		t.Errorf("TargetLanguage should be unchanged: got %q", got.TargetLanguage)
	}

	// Confirm persistence via GetSettings.
	got2, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings after update: %v", err)
	}
	if got2.CEFRLevel != newLevel {
		t.Errorf("CEFRLevel after reload: got %q, want %q", got2.CEFRLevel, newLevel)
	}
}

func TestUpdateSettings_MarkdownWarnThreshold(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	threshold := 3
	got, err := s.UpdateSettings(ctx, model.SettingsPatch{MarkdownWarnThreshold: &threshold})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if got.MarkdownWarnThreshold != threshold {
		t.Errorf("MarkdownWarnThreshold after update: got %d, want %d", got.MarkdownWarnThreshold, threshold)
	}

	// Confirm persistence via GetSettings.
	got2, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings after update: %v", err)
	}
	if got2.MarkdownWarnThreshold != threshold {
		t.Errorf("MarkdownWarnThreshold after reload: got %d, want %d", got2.MarkdownWarnThreshold, threshold)
	}
}

func TestUpdateSettings_EnrichmentPrompt(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// Default is empty (use built-in template).
	got, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if got.EnrichmentPrompt != "" {
		t.Errorf("default EnrichmentPrompt: got %q, want empty", got.EnrichmentPrompt)
	}

	prompt := "Custom prompt with {{cefr_level}} placeholder"
	got, err = s.UpdateSettings(ctx, model.SettingsPatch{EnrichmentPrompt: &prompt})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if got.EnrichmentPrompt != prompt {
		t.Errorf("EnrichmentPrompt after update: got %q, want %q", got.EnrichmentPrompt, prompt)
	}

	// Confirm persistence via GetSettings.
	got2, err := s.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings after update: %v", err)
	}
	if got2.EnrichmentPrompt != prompt {
		t.Errorf("EnrichmentPrompt after reload: got %q, want %q", got2.EnrichmentPrompt, prompt)
	}

	// Empty string resets to the built-in default.
	empty := ""
	got3, err := s.UpdateSettings(ctx, model.SettingsPatch{EnrichmentPrompt: &empty})
	if err != nil {
		t.Fatalf("UpdateSettings reset: %v", err)
	}
	if got3.EnrichmentPrompt != "" {
		t.Errorf("EnrichmentPrompt after reset: got %q, want empty", got3.EnrichmentPrompt)
	}
}

// ── Articles ──────────────────────────────────────────────────────────────────

func TestCreateArticle_And_GetByHash(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	a := makeArticle("https://example.com/article1")
	if err := s.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}

	got, err := s.GetArticleByHash(ctx, a.URLHash)
	if err != nil {
		t.Fatalf("GetArticleByHash: %v", err)
	}
	if got.ID != a.ID {
		t.Errorf("ID mismatch: got %q, want %q", got.ID, a.ID)
	}
	if got.Title != a.Title {
		t.Errorf("Title mismatch: got %q, want %q", got.Title, a.Title)
	}
	if len(got.Tokens) != 1 {
		t.Errorf("Tokens: got %d, want 1", len(got.Tokens))
	}
}

func TestCreateArticle_Dedup(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	a := makeArticle("https://example.com/dup")
	if err := s.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle first: %v", err)
	}

	// Second insert with same url_hash must return ErrDuplicate.
	a2 := makeArticle("https://example.com/dup")
	a2.ID = store.NewID() // different ID
	err := s.CreateArticle(ctx, a2)
	if err == nil {
		t.Fatal("expected ErrDuplicate, got nil")
	}
	if !isErr(err, ports.ErrDuplicate) {
		t.Errorf("expected ErrDuplicate, got %v", err)
	}
}

func TestGetArticleByHash_NotFound(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	_, err := s.GetArticleByHash(ctx, "nonexistent-hash")
	if !isErr(err, ports.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetArticle_NotFound(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	_, err := s.GetArticle(ctx, "nonexistent-id")
	if !isErr(err, ports.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListArticleMeta_AllAndSince(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Second)

	a1 := makeArticle("https://example.com/a1")
	a1.UpdatedAt = base.Add(-2 * time.Second)
	a2 := makeArticle("https://example.com/a2")
	a2.UpdatedAt = base.Add(2 * time.Second)

	for _, a := range []*model.Article{a1, a2} {
		if err := s.CreateArticle(ctx, a); err != nil {
			t.Fatalf("CreateArticle %s: %v", a.ID, err)
		}
	}

	// Full list.
	all, err := s.ListArticleMeta(ctx, time.Time{})
	if err != nil {
		t.Fatalf("ListArticleMeta all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 articles, got %d", len(all))
	}

	// Since cursor — should only return a2 (updated_at > base).
	since, err := s.ListArticleMeta(ctx, base)
	if err != nil {
		t.Fatalf("ListArticleMeta since: %v", err)
	}
	if len(since) != 1 {
		t.Errorf("expected 1 article since cursor, got %d", len(since))
	}
	if since[0].ID != a2.ID {
		t.Errorf("expected a2, got %q", since[0].ID)
	}
}

// ── Enrichment roundtrip ──────────────────────────────────────────────────────

func TestEnrichmentRoundtrip(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	a := makeArticle("https://example.com/enrich")
	if err := s.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}

	enrichment := model.Enrichment{
		DifficultWords: []model.DifficultWord{
			{TokenIndex: 0, Lemma: "hello", Translation: "привет", CEFRLevel: model.CEFRA2},
		},
		Phrases: []model.Phrase{
			{StartIndex: 0, EndIndex: 0, Type: model.PhraseTypeIdiom, Translation: "test"},
		},
		Sentences: []model.Sentence{
			{StartIndex: 0, EndIndex: 0, Translation: "Привет мир"},
		},
		Glossary: []model.GlossaryItem{
			{Term: "Hello", Definition: "A greeting"},
		},
	}
	enrichedAt := time.Now().UTC().Truncate(time.Second)
	if err := s.SaveEnrichment(ctx, a.ID, enrichment, enrichedAt); err != nil {
		t.Fatalf("SaveEnrichment: %v", err)
	}

	// Verify via GetArticlePayload.
	payload, err := s.GetArticlePayload(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetArticlePayload: %v", err)
	}
	if payload.Status != model.StatusEnriched {
		t.Errorf("status: got %q, want %q", payload.Status, model.StatusEnriched)
	}
	if payload.Enrichment == nil {
		t.Fatal("enrichment is nil")
	}
	if len(payload.Enrichment.DifficultWords) != 1 {
		t.Errorf("difficult_words: got %d, want 1", len(payload.Enrichment.DifficultWords))
	}
	if payload.Enrichment.DifficultWords[0].Translation != "привет" {
		t.Errorf("translation: got %q, want %q", payload.Enrichment.DifficultWords[0].Translation, "привет")
	}
	if len(payload.Enrichment.Phrases) != 1 {
		t.Errorf("phrases: got %d, want 1", len(payload.Enrichment.Phrases))
	}
	if len(payload.Enrichment.Sentences) != 1 {
		t.Errorf("sentences: got %d, want 1", len(payload.Enrichment.Sentences))
	}
	if len(payload.Enrichment.Glossary) != 1 {
		t.Errorf("glossary: got %d, want 1", len(payload.Enrichment.Glossary))
	}

	// The single token [0,0] is covered by the sentence [0,0], so coverage is
	// full both on the payload and on the library metadata projection.
	if payload.EnrichmentCoverage != 1.0 {
		t.Errorf("payload coverage: got %v, want 1.0", payload.EnrichmentCoverage)
	}
	metas, err := s.ListArticleMeta(ctx, time.Time{})
	if err != nil {
		t.Fatalf("ListArticleMeta: %v", err)
	}
	if len(metas) != 1 || metas[0].EnrichmentCoverage != 1.0 {
		t.Errorf("meta coverage: got %+v, want one row at 1.0", metas)
	}

	// Verify enriched_at via GetArticle.
	got, err := s.GetArticle(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetArticle after enrichment: %v", err)
	}
	if !got.EnrichedAt.Equal(enrichedAt) {
		t.Errorf("enriched_at: got %v, want %v", got.EnrichedAt, enrichedAt)
	}
}

// TestSaveEnrichment_PartialCoverage verifies that the sentence-coverage signal
// reflects a lazy/truncated LLM response: when the sentences only span the first
// half of the tokens, coverage is the covered fraction, not 1.0.
func TestSaveEnrichment_PartialCoverage(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	a := makeArticle("https://example.com/partial")
	// Four tokens; only the first two are covered by a sentence.
	a.Tokens = []model.Token{
		{Index: 0, Text: "a", Start: 0, End: 1},
		{Index: 1, Text: "b", Start: 1, End: 2},
		{Index: 2, Text: "c", Start: 2, End: 3},
		{Index: 3, Text: "d", Start: 3, End: 4},
	}
	if err := s.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}

	enrichment := model.Enrichment{
		Sentences: []model.Sentence{{StartIndex: 0, EndIndex: 1, Translation: "ab"}},
	}
	if err := s.SaveEnrichment(ctx, a.ID, enrichment, time.Now().UTC()); err != nil {
		t.Fatalf("SaveEnrichment: %v", err)
	}

	payload, err := s.GetArticlePayload(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetArticlePayload: %v", err)
	}
	if payload.EnrichmentCoverage != 0.5 {
		t.Errorf("coverage: got %v, want 0.5", payload.EnrichmentCoverage)
	}
}

// TestSaveEnrichment_CoverageEdgeCases exercises the clamping and de-duplication
// logic of the coverage computation through the public SaveEnrichment path: the
// LLM emits out-of-range, overlapping, or inverted token ranges, and the signal
// must stay in [0,1] and count each token at most once. All articles have four
// tokens.
func TestSaveEnrichment_CoverageEdgeCases(t *testing.T) {
	cases := []struct {
		name      string
		sentences []model.Sentence
		want      float64
	}{
		{"no sentences", nil, 0},
		{"out-of-range clamps to full", []model.Sentence{{StartIndex: -5, EndIndex: 100}}, 1.0},
		{"negative start clamps", []model.Sentence{{StartIndex: -2, EndIndex: 1}}, 0.5},
		{"end overflow clamps", []model.Sentence{{StartIndex: 2, EndIndex: 99}}, 0.5},
		{"overlap counted once", []model.Sentence{{StartIndex: 0, EndIndex: 1}, {StartIndex: 1, EndIndex: 2}}, 0.75},
		{"inverted range ignored", []model.Sentence{{StartIndex: 3, EndIndex: 1}}, 0},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openStore(t)
			ctx := context.Background()

			a := makeArticle(fmt.Sprintf("https://example.com/edge-%d", i))
			a.Tokens = []model.Token{
				{Index: 0, Text: "a", Start: 0, End: 1},
				{Index: 1, Text: "b", Start: 1, End: 2},
				{Index: 2, Text: "c", Start: 2, End: 3},
				{Index: 3, Text: "d", Start: 3, End: 4},
			}
			if err := s.CreateArticle(ctx, a); err != nil {
				t.Fatalf("CreateArticle: %v", err)
			}

			if err := s.SaveEnrichment(ctx, a.ID, model.Enrichment{Sentences: tc.sentences}, time.Now().UTC()); err != nil {
				t.Fatalf("SaveEnrichment: %v", err)
			}

			payload, err := s.GetArticlePayload(ctx, a.ID)
			if err != nil {
				t.Fatalf("GetArticlePayload: %v", err)
			}
			if payload.EnrichmentCoverage != tc.want {
				t.Errorf("coverage: got %v, want %v", payload.EnrichmentCoverage, tc.want)
			}
		})
	}
}

// ── LWW Progress ─────────────────────────────────────────────────────────────

func TestUpsertProgress_LWW(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	a := makeArticle("https://example.com/progress")
	if err := s.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}

	base := time.Now().UTC().Truncate(time.Second)

	// Initial insert.
	p1 := model.Progress{ArticleID: a.ID, Position: 100, IsRead: false, UpdatedAt: base}
	applied, err := s.UpsertProgress(ctx, p1)
	if err != nil {
		t.Fatalf("UpsertProgress initial: %v", err)
	}
	if !applied {
		t.Error("initial insert should be applied")
	}

	// Newer record — should win.
	p2 := model.Progress{ArticleID: a.ID, Position: 200, IsRead: true, UpdatedAt: base.Add(time.Second)}
	applied, err = s.UpsertProgress(ctx, p2)
	if err != nil {
		t.Fatalf("UpsertProgress newer: %v", err)
	}
	if !applied {
		t.Error("newer record should be applied")
	}

	// Older record — must be rejected.
	p3 := model.Progress{ArticleID: a.ID, Position: 50, IsRead: false, UpdatedAt: base.Add(-time.Second)}
	applied, err = s.UpsertProgress(ctx, p3)
	if err != nil {
		t.Fatalf("UpsertProgress older: %v", err)
	}
	if applied {
		t.Error("older record should NOT be applied")
	}

	// Equal timestamp — must be rejected (not strictly after).
	p4 := model.Progress{ArticleID: a.ID, Position: 300, IsRead: false, UpdatedAt: base.Add(time.Second)}
	applied, err = s.UpsertProgress(ctx, p4)
	if err != nil {
		t.Fatalf("UpsertProgress equal: %v", err)
	}
	if applied {
		t.Error("equal-timestamp record should NOT be applied")
	}

	// Confirm that the stored value is p2.
	progs, err := s.ListProgress(ctx, time.Time{})
	if err != nil {
		t.Fatalf("ListProgress: %v", err)
	}
	if len(progs) != 1 {
		t.Fatalf("expected 1 progress, got %d", len(progs))
	}
	if progs[0].Position != 200 {
		t.Errorf("position: got %d, want 200", progs[0].Position)
	}
	if !progs[0].IsRead {
		t.Error("is_read should be true")
	}
}

// ── Since cursor for ListProgress ─────────────────────────────────────────────

func TestListProgress_Since(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Second)

	a1 := makeArticle("https://example.com/p1")
	a1.UpdatedAt = base
	a2 := makeArticle("https://example.com/p2")
	a2.UpdatedAt = base
	for _, a := range []*model.Article{a1, a2} {
		if err := s.CreateArticle(ctx, a); err != nil {
			t.Fatalf("CreateArticle: %v", err)
		}
	}

	old := model.Progress{ArticleID: a1.ID, Position: 10, UpdatedAt: base.Add(-time.Second)}
	new := model.Progress{ArticleID: a2.ID, Position: 20, UpdatedAt: base.Add(time.Second)}
	for _, p := range []model.Progress{old, new} {
		if _, err := s.UpsertProgress(ctx, p); err != nil {
			t.Fatalf("UpsertProgress: %v", err)
		}
	}

	// Since base: only the newer record (base+1s > base).
	result, err := s.ListProgress(ctx, base)
	if err != nil {
		t.Fatalf("ListProgress since: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 progress since cursor, got %d", len(result))
	}
	if len(result) == 1 && result[0].ArticleID != a2.ID {
		t.Errorf("expected a2 progress, got %q", result[0].ArticleID)
	}
}

// ── Delete cascade ────────────────────────────────────────────────────────────

func TestDeleteArticle_Cascade(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	a := makeArticle("https://example.com/cascade")
	if err := s.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}

	// Insert enrichment.
	if err := s.SaveEnrichment(ctx, a.ID, model.Enrichment{}, time.Now().UTC()); err != nil {
		t.Fatalf("SaveEnrichment: %v", err)
	}

	// Insert progress.
	p := model.Progress{ArticleID: a.ID, Position: 1, UpdatedAt: time.Now().UTC().Truncate(time.Second)}
	if _, err := s.UpsertProgress(ctx, p); err != nil {
		t.Fatalf("UpsertProgress: %v", err)
	}

	// Delete article.
	if err := s.DeleteArticle(ctx, a.ID); err != nil {
		t.Fatalf("DeleteArticle: %v", err)
	}

	// Article should be gone.
	if _, err := s.GetArticle(ctx, a.ID); !isErr(err, ports.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}

	// Progress should be gone due to FK cascade.
	progs, err := s.ListProgress(ctx, time.Time{})
	if err != nil {
		t.Fatalf("ListProgress after delete: %v", err)
	}
	if len(progs) != 0 {
		t.Errorf("expected 0 progress after delete, got %d", len(progs))
	}

	// Payload should be gone.
	if _, err := s.GetArticlePayload(ctx, a.ID); !isErr(err, ports.ErrNotFound) {
		t.Errorf("expected ErrNotFound for payload after delete, got %v", err)
	}
}

func TestDeleteArticle_NotFound(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	err := s.DeleteArticle(ctx, "no-such-id")
	if !isErr(err, ports.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestSaveEnrichment_ArticleMissing verifies that saving an enrichment for an
// article that does not exist surfaces ErrNotFound (the FOREIGN KEY violation
// is mapped), rather than a raw constraint error. This is the TOCTOU race where
// an article is deleted while its enrichment is in flight.
func TestSaveEnrichment_ArticleMissing(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// Never-existed article.
	err := s.SaveEnrichment(ctx, "no-such-id", model.Enrichment{}, time.Now().UTC())
	if !isErr(err, ports.ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing article, got %v", err)
	}

	// Article that existed, then was deleted before the enrichment save.
	a := makeArticle("https://example.com/deleted-mid-enrich")
	if err := s.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}
	if err := s.DeleteArticle(ctx, a.ID); err != nil {
		t.Fatalf("DeleteArticle: %v", err)
	}
	if err := s.SaveEnrichment(ctx, a.ID, model.Enrichment{}, time.Now().UTC()); !isErr(err, ports.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

// ── RetryArticle ────────────────────────────────────────────────────────────

func TestRetryArticle(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	a := makeArticle("https://example.com/retry")
	if err := s.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}

	// Enrich it first, then mark the enrichment stage failed.
	if err := s.SaveEnrichment(ctx, a.ID, model.Enrichment{}, time.Now().UTC()); err != nil {
		t.Fatalf("SaveEnrichment: %v", err)
	}
	if err := s.SetStatus(ctx, a.ID, model.StatusEnrichFailed, "timeout"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	// Retry: an enrich_failed article goes back to fetched (re-enrich only).
	if err := s.RetryArticle(ctx, a.ID); err != nil {
		t.Fatalf("RetryArticle: %v", err)
	}

	got, err := s.GetArticle(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetArticle after retry: %v", err)
	}
	if got.Status != model.StatusFetched {
		t.Errorf("status: got %q, want %q", got.Status, model.StatusFetched)
	}
	if got.Error != "" {
		t.Errorf("error should be cleared, got %q", got.Error)
	}
	if !got.EnrichedAt.IsZero() {
		t.Errorf("enriched_at should be zero after retry, got %v", got.EnrichedAt)
	}
}

func TestRetryArticle_FetchFailedGoesToQueued(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	a := makeArticle("https://example.com/retry-fetch")
	if err := s.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}
	if err := s.SetStatus(ctx, a.ID, model.StatusFetchFailed, "blocked"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	if err := s.RetryArticle(ctx, a.ID); err != nil {
		t.Fatalf("RetryArticle: %v", err)
	}

	got, err := s.GetArticle(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetArticle: %v", err)
	}
	if got.Status != model.StatusQueued {
		t.Errorf("status: got %q, want %q", got.Status, model.StatusQueued)
	}
}

func TestRetryArticle_NotFound(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	err := s.RetryArticle(ctx, "no-such-id")
	if !isErr(err, ports.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ── SetPinned ──────────────────────────────────────────────────────────────

func TestSetPinned(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	a := makeArticle("https://example.com/pin")
	if err := s.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}

	// Fresh articles default to unpinned.
	got, err := s.GetArticle(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetArticle: %v", err)
	}
	if got.Pinned {
		t.Fatalf("new article should be unpinned")
	}

	// Pin it.
	if err := s.SetPinned(ctx, a.ID, true); err != nil {
		t.Fatalf("SetPinned(true): %v", err)
	}
	got, err = s.GetArticle(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetArticle after pin: %v", err)
	}
	if !got.Pinned {
		t.Errorf("article should be pinned after SetPinned(true)")
	}

	// The pinned flag also surfaces in the library projection.
	metas, err := s.ListArticleMeta(ctx, time.Time{})
	if err != nil {
		t.Fatalf("ListArticleMeta: %v", err)
	}
	if len(metas) != 1 || !metas[0].Pinned {
		t.Errorf("ListArticleMeta should report pinned=true, got %+v", metas)
	}

	// Unpin it.
	if err := s.SetPinned(ctx, a.ID, false); err != nil {
		t.Fatalf("SetPinned(false): %v", err)
	}
	got, err = s.GetArticle(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetArticle after unpin: %v", err)
	}
	if got.Pinned {
		t.Errorf("article should be unpinned after SetPinned(false)")
	}
}

func TestSetPinned_BumpsUpdatedAtForDeltaSync(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	a := makeArticle("https://example.com/pin-delta")
	// Backdate updated_at so the pin write produces a strictly newer timestamp,
	// proving the change rides a delta sync keyed on updated_at.
	a.UpdatedAt = time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	if err := s.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}

	since := a.UpdatedAt
	if err := s.SetPinned(ctx, a.ID, true); err != nil {
		t.Fatalf("SetPinned: %v", err)
	}

	// A delta list from the pre-pin cursor must include the just-pinned article.
	metas, err := s.ListArticleMeta(ctx, since)
	if err != nil {
		t.Fatalf("ListArticleMeta(since): %v", err)
	}
	if len(metas) != 1 || metas[0].ID != a.ID || !metas[0].Pinned {
		t.Errorf("delta sync should carry the pin change, got %+v", metas)
	}
}

func TestSetPinned_NotFound(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if err := s.SetPinned(ctx, "no-such-id", true); !isErr(err, ports.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ── ListWork ───────────────────────────────────────────────────────────────

func TestListWork(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		a := makeArticle(fmt.Sprintf("https://example.com/p%d", i))
		if err := s.CreateArticle(ctx, a); err != nil {
			t.Fatalf("CreateArticle %d: %v", i, err)
		}
	}

	// Enrich one of them — it leaves the work set.
	all, _ := s.ListArticleMeta(ctx, time.Time{})
	if err := s.SaveEnrichment(ctx, all[0].ID, model.Enrichment{}, time.Now().UTC()); err != nil {
		t.Fatalf("SaveEnrichment: %v", err)
	}

	work, err := s.ListWork(ctx, 10)
	if err != nil {
		t.Fatalf("ListWork: %v", err)
	}
	if len(work) != 4 {
		t.Errorf("expected 4 awaiting work, got %d", len(work))
	}

	// Limit works.
	limited, err := s.ListWork(ctx, 2)
	if err != nil {
		t.Fatalf("ListWork limited: %v", err)
	}
	if len(limited) != 2 {
		t.Errorf("expected 2 with limit 2, got %d", len(limited))
	}
}

// ── SaveContent ──────────────────────────────────────────────────────────────

func TestSaveContent(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// A queued article with no content yet.
	a := makeArticle("https://example.com/savecontent")
	a.Status = model.StatusQueued
	a.OriginalText = ""
	a.Tokens = nil
	if err := s.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}

	upd := ports.ContentUpdate{
		SourceURL:    "https://example.com/canonical",
		Title:        "Fetched Title",
		Author:       "Fetched Author",
		SourceDomain: "example.com",
		Lang:         "en",
		Text:         "Hello world",
		Tokens:       []model.Token{{Index: 0, Text: "Hello", Start: 0, End: 5}},
	}
	if err := s.SaveContent(ctx, a.ID, upd); err != nil {
		t.Fatalf("SaveContent: %v", err)
	}

	got, err := s.GetArticle(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetArticle: %v", err)
	}
	if got.Status != model.StatusFetched {
		t.Errorf("status: got %q, want %q", got.Status, model.StatusFetched)
	}
	if got.Title != upd.Title || got.OriginalText != upd.Text || len(got.Tokens) != 1 {
		t.Errorf("content not saved: %+v", got)
	}
}

func TestSaveContent_NotFound(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	err := s.SaveContent(ctx, "no-such-id", ports.ContentUpdate{})
	if !isErr(err, ports.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ── GetArticlePayload — no enrichment ─────────────────────────────────────────

func TestGetArticlePayload_NoEnrichment(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	a := makeArticle("https://example.com/noenrich")
	if err := s.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}

	payload, err := s.GetArticlePayload(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetArticlePayload: %v", err)
	}
	if payload.Enrichment != nil {
		t.Error("enrichment should be nil for not-yet-enriched article")
	}
	if payload.Status != model.StatusFetched {
		t.Errorf("status: got %q, want %q", payload.Status, model.StatusFetched)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// isErr reports whether err matches target via errors.Is.
func isErr(err, target error) bool {
	if err == nil {
		return false
	}
	// Direct check.
	if err == target {
		return true
	}
	// errors.Is unwrapping.
	type unwrapper interface{ Unwrap() error }
	type multiUnwrapper interface{ Unwrap() []error }
	if u, ok := err.(multiUnwrapper); ok {
		for _, e := range u.Unwrap() {
			if isErr(e, target) {
				return true
			}
		}
	}
	if u, ok := err.(unwrapper); ok {
		return isErr(u.Unwrap(), target)
	}
	return err.Error() == target.Error()
}

// --- Markdown budget --------------------------------------------------------

func TestMarkdownBudget(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// Fresh day starts at zero.
	used, err := s.MarkdownUnitsUsedToday(ctx)
	if err != nil {
		t.Fatalf("MarkdownUnitsUsedToday: %v", err)
	}
	if used != 0 {
		t.Fatalf("initial used = %d, want 0", used)
	}

	// Consume within budget.
	allowed, after, err := s.TryConsumeMarkdownUnits(ctx, 50, 120)
	if err != nil {
		t.Fatalf("TryConsumeMarkdownUnits: %v", err)
	}
	if !allowed || after != 50 {
		t.Fatalf("first consume: allowed=%v after=%d, want true/50", allowed, after)
	}

	allowed, after, err = s.TryConsumeMarkdownUnits(ctx, 50, 120)
	if err != nil {
		t.Fatalf("TryConsumeMarkdownUnits: %v", err)
	}
	if !allowed || after != 100 {
		t.Fatalf("second consume: allowed=%v after=%d, want true/100", allowed, after)
	}

	// Third would exceed 120 — rejected, counter unchanged.
	allowed, after, err = s.TryConsumeMarkdownUnits(ctx, 50, 120)
	if err != nil {
		t.Fatalf("TryConsumeMarkdownUnits: %v", err)
	}
	if allowed {
		t.Fatalf("third consume should be rejected, got allowed with after=%d", after)
	}
	if after != 100 {
		t.Fatalf("rejected consume reported after=%d, want 100", after)
	}

	// Refund returns units.
	if err := s.RefundMarkdownUnits(ctx, 50); err != nil {
		t.Fatalf("RefundMarkdownUnits: %v", err)
	}
	used, err = s.MarkdownUnitsUsedToday(ctx)
	if err != nil {
		t.Fatalf("MarkdownUnitsUsedToday: %v", err)
	}
	if used != 50 {
		t.Fatalf("used after refund = %d, want 50", used)
	}

	// Refund never goes below zero.
	if err := s.RefundMarkdownUnits(ctx, 1000); err != nil {
		t.Fatalf("RefundMarkdownUnits clamp: %v", err)
	}
	used, _ = s.MarkdownUnitsUsedToday(ctx)
	if used != 0 {
		t.Fatalf("used after over-refund = %d, want 0", used)
	}
}

func TestMarkdownBudgetUnlimited(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// dailyLimit <= 0 means unlimited.
	for i := 0; i < 5; i++ {
		allowed, _, err := s.TryConsumeMarkdownUnits(ctx, 1000, 0)
		if err != nil {
			t.Fatalf("TryConsumeMarkdownUnits: %v", err)
		}
		if !allowed {
			t.Fatalf("unlimited consume %d rejected", i)
		}
	}
}

// ── Auth: user + sessions ───────────────────────────────────────────────────

func TestUserLifecycle(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// Fresh DB is not initialized and has no user.
	if ok, err := s.IsInitialized(ctx); err != nil || ok {
		t.Fatalf("IsInitialized on fresh db = (%v, %v), want (false, nil)", ok, err)
	}
	if _, err := s.GetUser(ctx); err != ports.ErrNotFound {
		t.Fatalf("GetUser on fresh db err = %v, want ErrNotFound", err)
	}

	if err := s.CreateUser(ctx, "alice", "bcrypt-hash"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if ok, err := s.IsInitialized(ctx); err != nil || !ok {
		t.Fatalf("IsInitialized after create = (%v, %v), want (true, nil)", ok, err)
	}
	u, err := s.GetUser(ctx)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if u.Username != "alice" || u.PasswordHash != "bcrypt-hash" {
		t.Errorf("user = %+v, want alice/bcrypt-hash", u)
	}
	if u.CreatedAt.IsZero() || u.UpdatedAt.IsZero() {
		t.Error("timestamps should be set")
	}

	// Setup is one-time: a second CreateUser is rejected.
	if err := s.CreateUser(ctx, "bob", "other"); err != ports.ErrAlreadyInitialized {
		t.Fatalf("second CreateUser err = %v, want ErrAlreadyInitialized", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	const hash = "deadbeef"
	if ok, err := s.SessionExists(ctx, hash); err != nil || ok {
		t.Fatalf("SessionExists before create = (%v, %v), want (false, nil)", ok, err)
	}

	if err := s.CreateSession(ctx, hash, time.Now().UTC()); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Idempotent — creating the same session twice is fine.
	if err := s.CreateSession(ctx, hash, time.Now().UTC()); err != nil {
		t.Fatalf("CreateSession (repeat): %v", err)
	}

	if ok, err := s.SessionExists(ctx, hash); err != nil || !ok {
		t.Fatalf("SessionExists after create = (%v, %v), want (true, nil)", ok, err)
	}

	if err := s.DeleteSession(ctx, hash); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if ok, _ := s.SessionExists(ctx, hash); ok {
		t.Error("session should be gone after delete")
	}
	// Deleting a missing session is a no-op, not an error.
	if err := s.DeleteSession(ctx, "missing"); err != nil {
		t.Errorf("DeleteSession(missing) = %v, want nil", err)
	}
}
