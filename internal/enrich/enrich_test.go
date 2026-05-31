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
	// setStatusCalls counts SetStatus invocations per article id.
	setStatusCalls map[string]int
}

func newFakeStore(articles ...*model.Article) *fakeStore {
	s := &fakeStore{
		articles:       make(map[string]*model.Article),
		enrichments:    make(map[string]model.Enrichment),
		setStatusCalls: make(map[string]int),
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
	f.setStatusCalls[id]++
	a, ok := f.articles[id]
	if !ok {
		return ports.ErrNotFound
	}
	a.Status = status
	a.Error = errMsg
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

func (f *fakeStore) ListPending(_ context.Context, limit int) ([]model.Article, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Article
	for _, a := range f.articles {
		if a.Status == model.StatusPending {
			out = append(out, *a)
			if len(out) >= limit {
				break
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

func (f *fakeStore) RequeueArticle(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.articles[id]
	if !ok {
		return ports.ErrNotFound
	}
	a.Status = model.StatusPending
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

// setStatusCount returns how many times SetStatus was called for id.
func (f *fakeStore) setStatusCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.setStatusCalls[id]
}

// exists reports whether the article id is still present.
func (f *fakeStore) exists(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.articles[id]
	return ok
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
		Status: model.StatusPending,
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
	pool := enrich.NewPool(testCfg(1, 3), st, llm)

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
	pool := enrich.NewPool(testCfg(1, 5), st, llm)

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
	pool := enrich.NewPool(testCfg(1, 5), st, llm)

	ok := runPool(t, pool, 3*time.Second, func() bool {
		return st.status("article-3") == model.StatusFailed
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
	pool := enrich.NewPool(testCfg(1, maxRetries), st, llm)

	ok := runPool(t, pool, 10*time.Second, func() bool {
		return st.status("article-4") == model.StatusFailed
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
	pool := enrich.NewPool(testCfg(1, maxRetries), st, llm)

	ok := runPool(t, pool, 10*time.Second, func() bool {
		return st.status("article-5") == model.StatusFailed
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
	pool := enrich.NewPool(testCfg(1, maxRetries), st, llm)

	ok := runPool(t, pool, 10*time.Second, func() bool {
		return st.status("article-6") == model.StatusFailed
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
	pool := enrich.NewPool(testCfg(1, 1), st, llm)

	ok := runPool(t, pool, 10*time.Second, func() bool {
		return st.status("article-7") == model.StatusFailed
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
	pool := enrich.NewPool(testCfg(1, 3), st, llm)

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
	// markFailed must NOT be attempted for a deleted article.
	if n := st.setStatusCount("article-deleted"); n != 0 {
		t.Errorf("expected SetStatus not to be called for deleted article, got %d calls", n)
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
	pool := enrich.NewPool(testCfg(1, 3), st, llm)

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
	pool := enrich.NewPool(testCfg(2, 3), st, llm)

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
	pool := enrich.NewPool(testCfg(1, 0), st, llm)

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
