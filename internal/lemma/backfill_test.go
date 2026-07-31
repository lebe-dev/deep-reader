package lemma_test

import (
	"context"
	"errors"
	"testing"

	"deep-reader/internal/lemma"
	"deep-reader/internal/model"
	"deep-reader/internal/ports"
)

// backfillStub is an in-memory BackfillStore: articles carry a lemma_version
// and the stub serves whatever is still below the requested generation, exactly
// as the SQLite query does.
type backfillStub struct {
	articles  []model.Article
	versions  map[string]int
	saved     map[string][]model.Token
	saveErr   map[string]error
	listCalls int
	listErr   error
}

func newBackfillStub(articles ...model.Article) *backfillStub {
	return &backfillStub{
		articles: articles,
		versions: map[string]int{},
		saved:    map[string][]model.Token{},
		saveErr:  map[string]error{},
	}
}

func (b *backfillStub) ListArticlesForLemmaBackfill(_ context.Context, version, limit int) ([]model.Article, error) {
	b.listCalls++
	if b.listErr != nil {
		return nil, b.listErr
	}
	var out []model.Article
	for _, a := range b.articles {
		if b.versions[a.ID] >= version {
			continue
		}
		// Hand out a copy so a mutating annotator cannot corrupt the source.
		copied := a
		copied.Tokens = append([]model.Token(nil), a.Tokens...)
		out = append(out, copied)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (b *backfillStub) SaveTokenLemmas(_ context.Context, id string, tokens []model.Token, version int) error {
	if err := b.saveErr[id]; err != nil {
		return err
	}
	b.saved[id] = tokens
	b.versions[id] = version
	return nil
}

func article(id string, tokens ...model.Token) model.Article {
	return model.Article{ID: id, Tokens: tokens}
}

func TestRunBackfillAnnotatesAndStamps(t *testing.T) {
	l := newLemmatizer(t)
	st := newBackfillStub(article("a1",
		model.Token{Index: 0, Text: "The", Start: 0, End: 3},
		model.Token{Index: 1, Text: "children", Start: 4, End: 12},
		model.Token{Index: 2, Text: "ran", Start: 13, End: 16},
	))

	l.RunBackfill(context.Background(), st, nil)

	got := st.saved["a1"]
	if len(got) != 3 {
		t.Fatalf("saved %d tokens, want 3", len(got))
	}
	if got[1].Lemma != "child" || got[2].Lemma != "run" {
		t.Errorf("lemmas not filled: %+v", got)
	}
	if st.versions["a1"] != lemma.CurrentVersion {
		t.Errorf("lemma_version = %d, want %d", st.versions["a1"], lemma.CurrentVersion)
	}
}

func TestRunBackfillPreservesTokenCountIndicesAndOffsets(t *testing.T) {
	l := newLemmatizer(t)
	original := []model.Token{
		{Index: 0, Text: "Mice", Start: 0, End: 4},
		{Index: 1, Text: "were", Start: 5, End: 9},
		{Index: 2, Text: "running", Start: 10, End: 17},
	}
	st := newBackfillStub(article("a1", original...))

	l.RunBackfill(context.Background(), st, nil)

	got := st.saved["a1"]
	if len(got) != len(original) {
		t.Fatalf("token count changed: %d → %d — enrichment indices would break",
			len(original), len(got))
	}
	for i, want := range original {
		if got[i].Index != want.Index || got[i].Text != want.Text ||
			got[i].Start != want.Start || got[i].End != want.End {
			t.Errorf("token %d changed beyond Lemma: %+v → %+v", i, want, got[i])
		}
	}
}

func TestRunBackfillIsResumableAndRerunIsNoOp(t *testing.T) {
	l := newLemmatizer(t)
	st := newBackfillStub(
		article("a1", model.Token{Index: 0, Text: "children"}),
		article("a2", model.Token{Index: 0, Text: "mice"}),
	)

	l.RunBackfill(context.Background(), st, nil)
	if len(st.saved) != 2 {
		t.Fatalf("saved %d articles, want 2", len(st.saved))
	}

	// A second pass finds nothing left to do: one list call, no writes.
	callsBefore := st.listCalls
	st.saved = map[string][]model.Token{}
	l.RunBackfill(context.Background(), st, nil)
	if len(st.saved) != 0 {
		t.Errorf("re-run rewrote %d articles, want 0", len(st.saved))
	}
	if st.listCalls != callsBefore+1 {
		t.Errorf("re-run made %d list calls, want 1", st.listCalls-callsBefore)
	}
}

func TestRunBackfillSkipsFailingArticleAndKeepsGoing(t *testing.T) {
	l := newLemmatizer(t)
	st := newBackfillStub(
		article("gone", model.Token{Index: 0, Text: "children"}),
		article("ok", model.Token{Index: 0, Text: "mice"}),
	)
	// A concurrently deleted article must not stall the pass.
	st.saveErr["gone"] = ports.ErrNotFound

	l.RunBackfill(context.Background(), st, nil)

	if _, ok := st.saved["ok"]; !ok {
		t.Error("a failing article stopped the backfill")
	}
	if st.versions["gone"] != 0 {
		t.Error("a failed article was stamped as done")
	}
}

func TestRunBackfillStopsOnCancelledContext(t *testing.T) {
	l := newLemmatizer(t)
	st := newBackfillStub(article("a1", model.Token{Index: 0, Text: "children"}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	l.RunBackfill(ctx, st, nil)

	if st.listCalls != 0 || len(st.saved) != 0 {
		t.Errorf("cancelled backfill still did work: %d list calls, %d writes",
			st.listCalls, len(st.saved))
	}
}

func TestRunBackfillGivesUpOnListError(t *testing.T) {
	l := newLemmatizer(t)
	st := newBackfillStub(article("a1", model.Token{Index: 0, Text: "children"}))
	st.listErr = errors.New("database is locked")

	// Returns rather than spinning; the next boot retries.
	l.RunBackfill(context.Background(), st, nil)

	if len(st.saved) != 0 {
		t.Errorf("wrote %d articles despite a list failure", len(st.saved))
	}
}
