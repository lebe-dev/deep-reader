package markdown

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"deep-reader/internal/config"
	"deep-reader/internal/ports"
)

func testConfig(baseURL string) *config.Config {
	return &config.Config{
		MarkdownEnabled:        true,
		MarkdownBaseURL:        baseURL,
		MarkdownTimeout:        5 * time.Second,
		MarkdownDailyLimit:     500,
		MarkdownCostPerArticle: 50,
	}
}

// --- Client -----------------------------------------------------------------

func TestClientExtractSuccess(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-rate-limit-remaining", "450")
		_, _ = w.Write([]byte(`{"success":true,"url":"https://example.com/post","title":"Example Post","content":"# Example Post\n\nHello [world](https://x.test) here."}`))
	}))
	defer srv.Close()

	c := New(testConfig(srv.URL))
	res, err := c.Extract(context.Background(), "https://example.com/post")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Title != "Example Post" {
		t.Errorf("title = %q", res.Title)
	}
	if res.CanonicalURL != "https://example.com/post" {
		t.Errorf("canonical = %q", res.CanonicalURL)
	}
	if res.Domain != "example.com" {
		t.Errorf("domain = %q", res.Domain)
	}
	want := "Example Post\n\nHello world here."
	if res.Text != want {
		t.Errorf("text = %q, want %q", res.Text, want)
	}
}

func TestClientExtractRateLimited(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := New(testConfig(srv.URL))
	_, err := c.Extract(context.Background(), "https://example.com")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
	if !errors.Is(err, ErrConversionFailed) {
		t.Fatalf("want ErrConversionFailed wrapped, got %v", err)
	}
}

func TestClientExtractEmptyContent(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"url":"https://example.com","title":"x","content":"   \n\n"}`))
	}))
	defer srv.Close()

	c := New(testConfig(srv.URL))
	_, err := c.Extract(context.Background(), "https://example.com")
	if !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("want ErrEmptyContent, got %v", err)
	}
}

func TestClientExtractRejectsBadScheme(t *testing.T) {
	t.Parallel()
	c := New(testConfig("https://markdown.new"))
	if _, err := c.Extract(context.Background(), "ftp://example.com/file"); err == nil {
		t.Fatal("expected scheme error, got nil")
	}
}

// --- Chain ------------------------------------------------------------------

// fakeExtractor returns a canned result or error and records call count.
type fakeExtractor struct {
	calls  int
	result *ports.ExtractResult
	err    error
}

func (f *fakeExtractor) Extract(context.Context, string) (*ports.ExtractResult, error) {
	f.calls++
	return f.result, f.err
}

// fakeBudget is an in-memory Budget.
type fakeBudget struct {
	allow      bool
	consumed   int
	refunded   int
	consumeErr error
}

func (b *fakeBudget) TryConsumeMarkdownUnits(_ context.Context, cost, _ int) (bool, int, error) {
	if b.consumeErr != nil {
		return false, 0, b.consumeErr
	}
	if !b.allow {
		return false, 0, nil
	}
	b.consumed += cost
	return true, b.consumed, nil
}

func (b *fakeBudget) RefundMarkdownUnits(_ context.Context, cost int) error {
	b.refunded += cost
	return nil
}

func TestChainPrimarySuccess(t *testing.T) {
	t.Parallel()

	primary := &fakeExtractor{result: &ports.ExtractResult{Title: "from markdown"}}
	fallback := &fakeExtractor{result: &ports.ExtractResult{Title: "from readability"}}
	budget := &fakeBudget{allow: true}

	chain := NewChain(primary, fallback, budget, testConfig("https://markdown.new"))
	res, err := chain.Extract(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Title != "from markdown" {
		t.Errorf("expected primary result, got %q", res.Title)
	}
	if primary.calls != 1 || fallback.calls != 0 {
		t.Errorf("primary=%d fallback=%d", primary.calls, fallback.calls)
	}
	if budget.consumed != 50 || budget.refunded != 0 {
		t.Errorf("consumed=%d refunded=%d", budget.consumed, budget.refunded)
	}
}

func TestChainBudgetExhaustedUsesFallback(t *testing.T) {
	t.Parallel()

	primary := &fakeExtractor{result: &ports.ExtractResult{Title: "from markdown"}}
	fallback := &fakeExtractor{result: &ports.ExtractResult{Title: "from readability"}}
	budget := &fakeBudget{allow: false}

	chain := NewChain(primary, fallback, budget, testConfig("https://markdown.new"))
	res, err := chain.Extract(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Title != "from readability" {
		t.Errorf("expected fallback result, got %q", res.Title)
	}
	if primary.calls != 0 || fallback.calls != 1 {
		t.Errorf("primary=%d fallback=%d", primary.calls, fallback.calls)
	}
}

func TestChainPrimaryFailureRefundsAndFallsBack(t *testing.T) {
	t.Parallel()

	primary := &fakeExtractor{err: ErrConversionFailed}
	fallback := &fakeExtractor{result: &ports.ExtractResult{Title: "from readability"}}
	budget := &fakeBudget{allow: true}

	chain := NewChain(primary, fallback, budget, testConfig("https://markdown.new"))
	res, err := chain.Extract(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Title != "from readability" {
		t.Errorf("expected fallback result, got %q", res.Title)
	}
	if primary.calls != 1 || fallback.calls != 1 {
		t.Errorf("primary=%d fallback=%d", primary.calls, fallback.calls)
	}
	if budget.consumed != 50 || budget.refunded != 50 {
		t.Errorf("consumed=%d refunded=%d (expected both 50)", budget.consumed, budget.refunded)
	}
}

func TestChainBudgetErrorUsesFallback(t *testing.T) {
	t.Parallel()

	primary := &fakeExtractor{result: &ports.ExtractResult{Title: "from markdown"}}
	fallback := &fakeExtractor{result: &ports.ExtractResult{Title: "from readability"}}
	budget := &fakeBudget{consumeErr: errors.New("db down")}

	chain := NewChain(primary, fallback, budget, testConfig("https://markdown.new"))
	res, err := chain.Extract(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if res.Title != "from readability" {
		t.Errorf("expected fallback result, got %q", res.Title)
	}
	if primary.calls != 0 {
		t.Errorf("primary should not be called on budget error, calls=%d", primary.calls)
	}
}
