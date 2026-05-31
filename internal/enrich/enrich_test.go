package enrich_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"deep-reader/internal/config"
	"deep-reader/internal/enrich"
	"deep-reader/internal/model"
	"deep-reader/internal/ports"
)

// ---------------------------------------------------------------------------
// Fake store
// ---------------------------------------------------------------------------

type fakeStore struct {
	mu       sync.Mutex
	articles map[string]*model.Article
	// enrichments stores the last enrichment saved per article.
	enrichments map[string]model.Enrichment
	// settings is returned by GetSettings.
	settings model.Settings
	// failedCalls counts SetStatus invocations that set a *_failed status.
	failedCalls map[string]int
}

func newFakeStore(articles ...*model.Article) *fakeStore {
	s := &fakeStore{
		articles:    make(map[string]*model.Article),
		enrichments: make(map[string]model.Enrichment),
		failedCalls: make(map[string]int),
		settings: model.Settings{
			CEFRLevel:                model.CEFRA2,
			TargetLanguage:           model.DefaultTargetLanguage,
			LLMModel:                 "test-model",
			MinDifficultyToHighlight: model.CEFRB1,
		},
	}
	for _, a := range articles {
		cp := *a
		s.articles[a.ID] = &cp
	}
	return s
}

func (f *fakeStore) GetSettings(_ context.Context) (model.Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settings, nil
}

func (f *fakeStore) UpdateSettings(_ context.Context, _ model.SettingsPatch) (model.Settings, error) {
	return model.Settings{}, nil
}

func (f *fakeStore) CreateArticle(_ context.Context, a *model.Article) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *a
	f.articles[a.ID] = &cp
	return nil
}

func (f *fakeStore) GetArticleByHash(_ context.Context, _ string) (*model.Article, error) {
	return nil, ports.ErrNotFound
}

func (f *fakeStore) ListArticleMeta(_ context.Context, _ time.Time) ([]model.ArticleMeta, error) {
	return nil, nil
}

func (f *fakeStore) GetArticle(_ context.Context, id string) (*model.Article, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.articles[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	cp := *a
	return &cp, nil
}

func (f *fakeStore) GetArticlePayload(_ context.Context, _ string) (*model.ArticlePayload, error) {
	return nil, ports.ErrNotFound
}

func (f *fakeStore) DeleteArticle(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.articles[id]; !ok {
		return ports.ErrNotFound
	}
	delete(f.articles, id)
	return nil
}

func (f *fakeStore) SetStatus(_ context.Context, id, status, errMsg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if status == model.StatusFetchFailed || status == model.StatusEnrichFailed {
		f.failedCalls[id]++
	}
	a, ok := f.articles[id]
	if !ok {
		return ports.ErrNotFound
	}
	a.Status = status
	a.Error = errMsg
	return nil
}

func (f *fakeStore) SaveContent(_ context.Context, id string, c ports.ContentUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.articles[id]
	if !ok {
		return ports.ErrNotFound
	}
	a.SourceURL = c.SourceURL
	a.Title = c.Title
	a.Author = c.Author
	a.SourceDomain = c.SourceDomain
	a.Lang = c.Lang
	a.OriginalText = c.Text
	a.Tokens = c.Tokens
	a.Status = model.StatusFetched
	a.Error = ""
	return nil
}

func (f *fakeStore) SaveEnrichment(_ context.Context, id string, e model.Enrichment, enrichedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.articles[id]
	if !ok {
		return ports.ErrNotFound
	}
	a.Status = model.StatusEnriched
	a.EnrichedAt = enrichedAt
	f.enrichments[id] = e
	return nil
}

func (f *fakeStore) ListWork(_ context.Context, limit int) ([]model.Article, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Article
	for _, a := range f.articles {
		switch a.Status {
		case model.StatusQueued, model.StatusFetching, model.StatusFetched, model.StatusEnriching:
			out = append(out, *a)
			if len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}

func (f *fakeStore) UpsertProgress(_ context.Context, _ model.Progress) (bool, error) {
	return true, nil
}

func (f *fakeStore) ListProgress(_ context.Context, _ time.Time) ([]model.Progress, error) {
	return nil, nil
}

func (f *fakeStore) RetryArticle(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.articles[id]
	if !ok {
		return ports.ErrNotFound
	}
	if a.Status == model.StatusEnrichFailed {
		a.Status = model.StatusFetched
	} else {
		a.Status = model.StatusQueued
	}
	a.Error = ""
	return nil
}

func (f *fakeStore) MarkdownUnitsUsedToday(_ context.Context) (int, error) { return 0, nil }
func (f *fakeStore) TryConsumeMarkdownUnits(_ context.Context, _, _ int) (bool, int, error) {
	return true, 0, nil
}
func (f *fakeStore) RefundMarkdownUnits(_ context.Context, _ int) error { return nil }

// status is a helper for tests to read article status without the mutex.
func (f *fakeStore) status(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.articles[id]; ok {
		return a.Status
	}
	return ""
}

// errMsg is a helper for tests to read the stored error message.
func (f *fakeStore) errMsg(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.articles[id]; ok {
		return a.Error
	}
	return ""
}

// savedEnrichment returns the enrichment last saved for id, or false if none.
func (f *fakeStore) savedEnrichment(id string) (model.Enrichment, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.enrichments[id]
	return e, ok
}

// exists reports whether the article id is still present.
func (f *fakeStore) exists(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.articles[id]
	return ok
}

// failedCount returns how many times a *_failed status was set for id.
func (f *fakeStore) failedCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failedCalls[id]
}

// originalText returns the stored original text for id.
func (f *fakeStore) originalText(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.articles[id]; ok {
		return a.OriginalText
	}
	return ""
}

// ---------------------------------------------------------------------------
// Fake extractor
// ---------------------------------------------------------------------------

// fakeExtractor is a configurable fake of ports.Extractor. The zero value
// returns a canned two-token result; set result or err to override. It records
// the number of Extract calls.
type fakeExtractor struct {
	mu     sync.Mutex
	calls  int
	result *ports.ExtractResult
	err    error
}

func (f *fakeExtractor) Extract(_ context.Context, rawURL string) (*ports.ExtractResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &ports.ExtractResult{
		CanonicalURL: rawURL,
		Title:        "Test Article",
		Author:       "Author",
		Domain:       "example.com",
		Lang:         "en",
		Text:         "Hello world",
	}, nil
}

func (f *fakeExtractor) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// ---------------------------------------------------------------------------
// Fake LLM client
// ---------------------------------------------------------------------------

// fakeError implements the retryable error interface mirroring llm.APIError.
type fakeError struct {
	msg       string
	retryable bool
}

func (e *fakeError) Error() string   { return e.msg }
func (e *fakeError) Retryable() bool { return e.retryable }

// fakeLLM is a configurable fake that can inject errors a fixed number of
// times before returning a success result.
type fakeLLM struct {
	mu sync.Mutex
	// callCount tracks the number of Enrich invocations.
	callCount int
	// failN is the number of leading calls that should fail with failErr.
	failN   int
	failErr error
	// result is what to return on success.
	result *model.Enrichment
	// onEnrich, if set, runs at the start of each Enrich call. Used by tests to
	// inject side effects (e.g. deleting the article mid-enrichment).
	onEnrich func()
}

func (f *fakeLLM) Enrich(_ context.Context, _ *model.Article, _ model.Settings, _ int) (*model.Enrichment, ports.Usage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++
	if f.onEnrich != nil {
		f.onEnrich()
	}
	if f.callCount <= f.failN {
		return nil, ports.Usage{}, f.failErr
	}
	return f.result, ports.Usage{
		PromptTokens:     10,
		CompletionTokens: 20,
		TotalTokens:      30,
	}, nil
}

func (f *fakeLLM) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callCount
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func testCfg(maxConcurrent, maxRetries int) *config.Config {
	return &config.Config{
		HTTPPort:           8080,
		AuthToken:          "test",
		DatabasePath:       "/tmp/test.db",
		LLMAPIBaseURL:      "http://localhost",
		LLMAPIKey:          "key",
		LLMModel:           "test",
		LLMMaxConcurrent:   maxConcurrent,
		LLMRequestTimeout:  5 * time.Second,
		LLMMaxRetries:      maxRetries,
		ReadabilityTimeout: 10 * time.Second,
		EnrichmentVersion:  1,
		LogLevel:           "info",
		LogFormat:          "json",
	}
}

func makeArticle(id string, tokenCount int) *model.Article {
	tokens := make([]model.Token, tokenCount)
	for i := range tokens {
		tokens[i] = model.Token{Index: i, Text: "word", Start: i * 5, End: i*5 + 4}
	}
	return &model.Article{
		ID:     id,
		Title:  "Test Article",
		Status: model.StatusFetched,
		Tokens: tokens,
	}
}

// goodEnrichment returns a valid enrichment for an article with tokenCount >= 2.
func goodEnrichment(tokenCount int) *model.Enrichment {
	if tokenCount < 2 {
		tokenCount = 2
	}
	return &model.Enrichment{
		DifficultWords: []model.DifficultWord{
			{TokenIndex: 0, Lemma: "word", Translation: "слово", CEFRLevel: model.CEFRB2},
		},
		Phrases: []model.Phrase{
			{StartIndex: 0, EndIndex: tokenCount - 1, Type: model.PhraseTypeIdiom, Translation: "фраза"},
		},
		Sentences: []model.Sentence{
			{StartIndex: 0, EndIndex: tokenCount - 1, Translation: "предложение"},
		},
		Glossary: []model.GlossaryItem{
			{Term: "term", Definition: "definition"},
		},
	}
}

// runPool starts the pool, calls notify, and waits until either the condition
// fn is true or a timeout is reached. Returns whether fn was satisfied.
func runPool(t *testing.T, pool *enrich.Pool, deadline time.Duration, fn func() bool) bool {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	poolDone := make(chan struct{})
	go func() {
		defer close(poolDone)
		pool.Start(ctx)
	}()

	// Poll until condition is met or deadline is reached.
	dl := time.Now().Add(deadline)
	for time.Now().Before(dl) {
		if fn() {
			cancel()
			<-poolDone
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-poolDone
	return fn()
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestSuccess verifies that a pending article is enriched and saved correctly.
func TestSuccess(t *testing.T) {
	article := makeArticle("article-1", 5)
	st := newFakeStore(article)
	llm := &fakeLLM{
		result: goodEnrichment(5),
	}
	pool := enrich.NewPool(testCfg(1, 3), st, &fakeExtractor{}, llm)

	ok := runPool(t, pool, 3*time.Second, func() bool {
		return st.status("article-1") == model.StatusEnriched
	})

	if !ok {
		t.Fatalf("expected status=enriched, got %q", st.status("article-1"))
	}
	e, saved := st.savedEnrichment("article-1")
	if !saved {
		t.Fatal("expected enrichment to be saved")
	}
	if len(e.DifficultWords) != 1 {
		t.Errorf("expected 1 difficult word, got %d", len(e.DifficultWords))
	}
	if len(e.Sentences) != 1 {
		t.Errorf("expected 1 sentence, got %d", len(e.Sentences))
	}
}

// TestTransientErrorRetriesThenSucceeds verifies that a transient error causes
// retries and the article ends up enriched on the final successful attempt.
func TestTransientErrorRetriesThenSucceeds(t *testing.T) {
	article := makeArticle("article-2", 3)
	st := newFakeStore(article)
	// Fail 2 times with a retryable error, then succeed.
	llm := &fakeLLM{
		failN:   2,
		failErr: &fakeError{msg: "rate limit", retryable: true},
		result:  goodEnrichment(3),
	}
	pool := enrich.NewPool(testCfg(1, 5), st, &fakeExtractor{}, llm)

	ok := runPool(t, pool, 10*time.Second, func() bool {
		return st.status("article-2") == model.StatusEnriched
	})

	if !ok {
		t.Fatalf("expected status=enriched after transient retries, got %q", st.status("article-2"))
	}
	if llm.calls() < 3 {
		t.Errorf("expected at least 3 LLM calls (2 fails + 1 success), got %d", llm.calls())
	}
}

// TestPermanentErrorMarksFailed verifies that a non-retryable error causes the
// article to be marked as failed without exhausting retries.
func TestPermanentErrorMarksFailed(t *testing.T) {
	article := makeArticle("article-3", 3)
	st := newFakeStore(article)
	llm := &fakeLLM{
		failN:   100, // always fail
		failErr: &fakeError{msg: "bad request", retryable: false},
	}
	pool := enrich.NewPool(testCfg(1, 5), st, &fakeExtractor{}, llm)

	ok := runPool(t, pool, 3*time.Second, func() bool {
		return st.status("article-3") == model.StatusEnrichFailed
	})

	if !ok {
		t.Fatalf("expected status=failed, got %q", st.status("article-3"))
	}
	if llm.calls() != 1 {
		t.Errorf("expected exactly 1 LLM call for permanent error, got %d", llm.calls())
	}
	if st.errMsg("article-3") == "" {
		t.Error("expected error message to be stored")
	}
}

// TestExhaustedRetriesMarksFailed verifies that after LLMMaxRetries retryable
// errors the article ends up as failed.
func TestExhaustedRetriesMarksFailed(t *testing.T) {
	article := makeArticle("article-4", 3)
	st := newFakeStore(article)
	maxRetries := 2
	// Always fail with a retryable error — more failures than maxRetries.
	llm := &fakeLLM{
		failN:   maxRetries + 10,
		failErr: &fakeError{msg: "server error", retryable: true},
		result:  goodEnrichment(3),
	}
	pool := enrich.NewPool(testCfg(1, maxRetries), st, &fakeExtractor{}, llm)

	ok := runPool(t, pool, 10*time.Second, func() bool {
		return st.status("article-4") == model.StatusEnrichFailed
	})

	if !ok {
		t.Fatalf("expected status=failed after exhausting retries, got %q", st.status("article-4"))
	}
	// Should have attempted maxRetries+1 total calls (initial + retries).
	if llm.calls() != maxRetries+1 {
		t.Errorf("expected %d LLM calls, got %d", maxRetries+1, llm.calls())
	}
}

// TestInvalidIndicesRejected verifies that an enrichment with out-of-range
// token indices is treated as a validation error and retried (eventually failed).
func TestInvalidIndicesRejected(t *testing.T) {
	tokenCount := 3
	article := makeArticle("article-5", tokenCount)
	st := newFakeStore(article)

	// The LLM always returns an enrichment with an OOB index.
	badEnrichment := &model.Enrichment{
		DifficultWords: []model.DifficultWord{
			{TokenIndex: tokenCount + 5, Lemma: "x", Translation: "x", CEFRLevel: model.CEFRB1},
		},
	}
	llm := &fakeLLM{
		result: badEnrichment,
	}
	maxRetries := 2
	pool := enrich.NewPool(testCfg(1, maxRetries), st, &fakeExtractor{}, llm)

	ok := runPool(t, pool, 10*time.Second, func() bool {
		return st.status("article-5") == model.StatusEnrichFailed
	})

	if !ok {
		t.Fatalf("expected status=failed after invalid indices exhausted retries, got %q", st.status("article-5"))
	}
	// Verify the enrichment was NOT saved.
	if _, saved := st.savedEnrichment("article-5"); saved {
		t.Error("expected enrichment NOT to be saved for invalid article")
	}
}

// TestPhraseBoundsValidation verifies that start_index > end_index is rejected.
func TestPhraseBoundsValidation(t *testing.T) {
	tokenCount := 5
	article := makeArticle("article-6", tokenCount)
	st := newFakeStore(article)

	// Phrase where start > end.
	badEnrichment := &model.Enrichment{
		Phrases: []model.Phrase{
			{StartIndex: 3, EndIndex: 1, Type: model.PhraseTypeIdiom, Translation: "x"},
		},
	}
	llm := &fakeLLM{
		result: badEnrichment,
	}
	maxRetries := 1
	pool := enrich.NewPool(testCfg(1, maxRetries), st, &fakeExtractor{}, llm)

	ok := runPool(t, pool, 10*time.Second, func() bool {
		return st.status("article-6") == model.StatusEnrichFailed
	})

	if !ok {
		t.Fatalf("expected status=failed for start>end phrase, got %q", st.status("article-6"))
	}
}

// TestSentenceBoundsValidation verifies that sentence start_index > end_index is rejected.
func TestSentenceBoundsValidation(t *testing.T) {
	tokenCount := 4
	article := makeArticle("article-7", tokenCount)
	st := newFakeStore(article)

	badEnrichment := &model.Enrichment{
		Sentences: []model.Sentence{
			{StartIndex: 2, EndIndex: 0, Translation: "x"},
		},
	}
	llm := &fakeLLM{result: badEnrichment}
	pool := enrich.NewPool(testCfg(1, 1), st, &fakeExtractor{}, llm)

	ok := runPool(t, pool, 10*time.Second, func() bool {
		return st.status("article-7") == model.StatusEnrichFailed
	})

	if !ok {
		t.Fatalf("expected status=failed for sentence start>end, got %q", st.status("article-7"))
	}
}

// TestArticleDeletedDuringEnrichment verifies the TOCTOU race where an article
// is deleted after the worker fetched it but before the enrichment is saved.
// SaveEnrichment then reports ErrNotFound; the worker must treat this as a
// benign condition: no retry, and no attempt to mark the (gone) article failed.
func TestArticleDeletedDuringEnrichment(t *testing.T) {
	article := makeArticle("article-deleted", 3)
	st := newFakeStore(article)
	llm := &fakeLLM{
		result: goodEnrichment(3),
		// Delete the article during the LLM call, so the subsequent
		// SaveEnrichment hits a missing parent row (FK / not found).
		onEnrich: func() { _ = st.DeleteArticle(context.Background(), "article-deleted") },
	}
	pool := enrich.NewPool(testCfg(1, 3), st, &fakeExtractor{}, llm)

	ok := runPool(t, pool, 3*time.Second, func() bool {
		return llm.calls() >= 1 && !st.exists("article-deleted")
	})
	if !ok {
		t.Fatal("expected the article to be processed once and gone")
	}

	// Give the worker a moment to finish processing the (now missing) article.
	time.Sleep(50 * time.Millisecond)

	// The LLM must have been called exactly once: a missing article is a
	// permanent condition, not a retryable one.
	if llm.calls() != 1 {
		t.Errorf("expected exactly 1 LLM call, got %d", llm.calls())
	}
	// A failed status must NOT be recorded for a deleted article (the in-flight
	// enriching flip may happen, but the missing article is a benign condition).
	if n := st.failedCount("article-deleted"); n != 0 {
		t.Errorf("expected no failed status for deleted article, got %d", n)
	}
	// No enrichment should have been persisted.
	if _, saved := st.savedEnrichment("article-deleted"); saved {
		t.Error("expected no enrichment saved for deleted article")
	}
}

// TestNotifyWakesWorker verifies that Notify() causes the worker to pick up a
// pending article that was added after Start.
func TestNotifyWakesWorker(t *testing.T) {
	// Start with an empty store.
	st := newFakeStore()
	llm := &fakeLLM{result: goodEnrichment(2)}
	pool := enrich.NewPool(testCfg(1, 3), st, &fakeExtractor{}, llm)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	poolDone := make(chan struct{})
	go func() {
		defer close(poolDone)
		pool.Start(ctx)
	}()

	// Add an article after the pool is running and notify.
	time.Sleep(20 * time.Millisecond)
	article := makeArticle("article-notify", 2)
	if err := st.CreateArticle(ctx, article); err != nil {
		t.Fatalf("create article: %v", err)
	}
	pool.Notify()

	// Wait for enrichment.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st.status("article-notify") == model.StatusEnriched {
			cancel()
			<-poolDone
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-poolDone
	if st.status("article-notify") != model.StatusEnriched {
		t.Fatalf("article was not enriched after Notify: status=%q", st.status("article-notify"))
	}
}

// TestContextCancellationStopsWorkers verifies that cancelling the context
// causes Start to return.
func TestContextCancellationStopsWorkers(t *testing.T) {
	st := newFakeStore()
	llm := &fakeLLM{result: goodEnrichment(2)}
	pool := enrich.NewPool(testCfg(2, 3), st, &fakeExtractor{}, llm)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		pool.Start(ctx)
	}()

	cancel()
	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after context cancellation")
	}
}

// TestNotifyIsNonBlocking verifies that calling Notify multiple times rapidly
// does not block.
func TestNotifyIsNonBlocking(t *testing.T) {
	st := newFakeStore()
	llm := &fakeLLM{result: goodEnrichment(2)}
	pool := enrich.NewPool(testCfg(1, 0), st, &fakeExtractor{}, llm)

	// Should not block even with many calls.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			pool.Notify()
		}
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Notify blocked unexpectedly")
	}
}

// ---------------------------------------------------------------------------
// Unit tests for validation helpers (not testing the pool).
// ---------------------------------------------------------------------------

// TestValidateEnrichmentOOBDifficultWord ensures an OOB DifficultWord returns error.
func TestValidateEnrichmentOOBDifficultWord(t *testing.T) {
	e := &model.Enrichment{
		DifficultWords: []model.DifficultWord{
			{TokenIndex: 10, Lemma: "x", Translation: "x", CEFRLevel: model.CEFRB1},
		},
	}
	if err := validateEnrichmentExported(e, 5); err == nil {
		t.Error("expected validation error for OOB token_index")
	}
}

// TestValidateEnrichmentNegativeIndex ensures a negative index returns error.
func TestValidateEnrichmentNegativeIndex(t *testing.T) {
	e := &model.Enrichment{
		DifficultWords: []model.DifficultWord{
			{TokenIndex: -1, Lemma: "x", Translation: "x", CEFRLevel: model.CEFRB1},
		},
	}
	if err := validateEnrichmentExported(e, 5); err == nil {
		t.Error("expected validation error for negative token_index")
	}
}

// TestValidateEnrichmentValidPasses ensures a fully valid enrichment is accepted.
func TestValidateEnrichmentValidPasses(t *testing.T) {
	e := goodEnrichment(5)
	if err := validateEnrichmentExported(e, 5); err != nil {
		t.Errorf("expected no validation error, got: %v", err)
	}
}

// TestValidateEnrichmentNil ensures nil enrichment is rejected.
func TestValidateEnrichmentNil(t *testing.T) {
	if err := validateEnrichmentExported(nil, 5); err == nil {
		t.Error("expected error for nil enrichment")
	}
}

// validateEnrichmentExported is the exported shim used by unit tests.
// The real function lives in enrich package; we expose it via export_test.go.
var validateEnrichmentExported = enrich.ValidateEnrichment

// BackoffDuration tests
func TestBackoffDurationGrowth(t *testing.T) {
	cases := []struct {
		attempt  int
		expected time.Duration
	}{
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
	}
	for _, tc := range cases {
		got := enrich.BackoffDuration(tc.attempt, 2*time.Second, 60*time.Second)
		if got != tc.expected {
			t.Errorf("attempt %d: expected %v, got %v", tc.attempt, tc.expected, got)
		}
	}
}

func TestBackoffDurationCap(t *testing.T) {
	cap := 10 * time.Second
	got := enrich.BackoffDuration(10, 2*time.Second, cap)
	if got != cap {
		t.Errorf("expected capped value %v, got %v", cap, got)
	}
}

// TestIsRetryable tests the retryable detection logic.
func TestIsRetryable(t *testing.T) {
	retryable := &fakeError{retryable: true}
	notRetryable := &fakeError{retryable: false}
	plain := errors.New("plain error")

	if !enrich.IsRetryable(retryable) {
		t.Error("expected retryable=true for fakeError{retryable:true}")
	}
	if enrich.IsRetryable(notRetryable) {
		t.Error("expected retryable=false for fakeError{retryable:false}")
	}
	if enrich.IsRetryable(plain) {
		t.Error("expected retryable=false for plain error")
	}
	if enrich.IsRetryable(nil) {
		t.Error("expected retryable=false for nil")
	}
}

// ---------------------------------------------------------------------------
// Fetch-stage tests
// ---------------------------------------------------------------------------

// queuedArticle returns an article in the queued state with a source URL but no
// content yet — the input to the fetch stage.
func queuedArticle(id, sourceURL string) *model.Article {
	return &model.Article{ID: id, SourceURL: sourceURL, Status: model.StatusQueued}
}

// TestFetchThenEnrich verifies the full pipeline: a queued article is fetched
// (content saved, status fetched) and then enriched (status enriched).
func TestFetchThenEnrich(t *testing.T) {
	st := newFakeStore(queuedArticle("article-fetch", "https://example.com/a"))
	ex := &fakeExtractor{}
	llm := &fakeLLM{result: goodEnrichment(2)}
	pool := enrich.NewPool(testCfg(1, 3), st, ex, llm)

	ok := runPool(t, pool, 3*time.Second, func() bool {
		return st.status("article-fetch") == model.StatusEnriched
	})
	if !ok {
		t.Fatalf("expected status=enriched, got %q", st.status("article-fetch"))
	}
	if ex.callCount() != 1 {
		t.Errorf("extractor calls: got %d, want 1", ex.callCount())
	}
	if got := st.originalText("article-fetch"); got != "Hello world" {
		t.Errorf("original text: got %q, want %q", got, "Hello world")
	}
	if _, saved := st.savedEnrichment("article-fetch"); !saved {
		t.Error("expected enrichment to be saved")
	}
}

// TestFetchDecodesHTMLEntities verifies the fetch stage decodes HTML entities
// before tokenizing, keeping token byte offsets consistent with the stored text.
func TestFetchDecodesHTMLEntities(t *testing.T) {
	st := newFakeStore(queuedArticle("article-entities", "https://example.com/a"))
	ex := &fakeExtractor{result: &ports.ExtractResult{
		CanonicalURL: "https://example.com/a",
		Title:        "Let&rsquo;s talk &mdash; musings",
		Text:         "First &amp; foremost it&rsquo;s loaded.",
		Domain:       "example.com",
		Lang:         "en",
	}}
	llm := &fakeLLM{result: &model.Enrichment{}}
	pool := enrich.NewPool(testCfg(1, 3), st, ex, llm)

	ok := runPool(t, pool, 3*time.Second, func() bool {
		return st.status("article-entities") == model.StatusEnriched
	})
	if !ok {
		t.Fatalf("expected status=enriched, got %q", st.status("article-entities"))
	}
	want := "First & foremost it’s loaded."
	if got := st.originalText("article-entities"); got != want {
		t.Errorf("decoded text:\n got %q\nwant %q", got, want)
	}
}

// TestFetchFailedMarksFetchFailed verifies a non-retryable extractor error marks
// the article fetch_failed and never calls the LLM.
func TestFetchFailedMarksFetchFailed(t *testing.T) {
	st := newFakeStore(queuedArticle("article-badfetch", "https://example.com/a"))
	ex := &fakeExtractor{err: &fakeError{msg: "blocked host", retryable: false}}
	llm := &fakeLLM{result: goodEnrichment(2)}
	pool := enrich.NewPool(testCfg(1, 5), st, ex, llm)

	ok := runPool(t, pool, 3*time.Second, func() bool {
		return st.status("article-badfetch") == model.StatusFetchFailed
	})
	if !ok {
		t.Fatalf("expected status=fetch_failed, got %q", st.status("article-badfetch"))
	}
	if llm.calls() != 0 {
		t.Errorf("LLM must not be called when fetch fails: got %d calls", llm.calls())
	}
	if ex.callCount() != 1 {
		t.Errorf("extractor calls for permanent error: got %d, want 1", ex.callCount())
	}
	if st.errMsg("article-badfetch") == "" {
		t.Error("expected error message to be stored")
	}
}

// TestRecoversInflightStatuses verifies an article stuck in an in-flight state
// (e.g. the server crashed mid-stage) is re-selected and driven to completion:
// fetching re-fetches, enriching re-enriches.
func TestRecoversInflightStatuses(t *testing.T) {
	// Both articles end up with 2 tokens (the default extractor yields a
	// two-token "Hello world"), so a single enrichment result is valid for both.
	fetching := queuedArticle("article-stuck-fetch", "https://example.com/a")
	fetching.Status = model.StatusFetching
	enriching := makeArticle("article-stuck-enrich", 2)
	enriching.Status = model.StatusEnriching
	st := newFakeStore(fetching, enriching)
	ex := &fakeExtractor{}
	llm := &fakeLLM{result: goodEnrichment(2)}
	pool := enrich.NewPool(testCfg(1, 3), st, ex, llm)

	ok := runPool(t, pool, 3*time.Second, func() bool {
		return st.status("article-stuck-fetch") == model.StatusEnriched &&
			st.status("article-stuck-enrich") == model.StatusEnriched
	})
	if !ok {
		t.Fatalf("expected both stuck articles enriched, got fetch=%q enrich=%q",
			st.status("article-stuck-fetch"), st.status("article-stuck-enrich"))
	}
}
