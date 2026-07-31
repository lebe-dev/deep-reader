package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"deep-reader/internal/model"
	"deep-reader/internal/ports"
	"deep-reader/internal/store"
)

// lookupAt builds a word lookup event at a distinct position.
func lookupAt(id, entryKey, articleID string, span int, surface string, at time.Time) model.LookupEvent {
	return model.LookupEvent{
		ID:           id,
		EntryKey:     entryKey,
		Kind:         model.LookupKindWord,
		ArticleID:    articleID,
		ArticleTitle: "Test Article",
		SpanStart:    span,
		SpanEnd:      span,
		Surface:      surface,
		Lemma:        "resilient",
		Translation:  "устойчивый",
		CEFRLevel:    model.CEFRB2,
		Context:      "proved remarkably resilient to shocks",
		OccurredAt:   at,
	}
}

func findEntry(entries []model.VocabEntry, key string) (model.VocabEntry, bool) {
	for _, e := range entries {
		if e.EntryKey == key {
			return e, true
		}
	}
	return model.VocabEntry{}, false
}

func TestSaveLookups_BuildsAggregate(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)

	accepted, err := s.SaveLookups(ctx, []model.LookupEvent{
		lookupAt("e1", "word:ru:resilient", "a1", 10, "resilient", at.Add(-2*time.Hour)),
		lookupAt("e2", "word:ru:resilient", "a1", 42, "resiliency", at),
	})
	if err != nil {
		t.Fatalf("SaveLookups: %v", err)
	}
	if accepted != 2 {
		t.Fatalf("accepted = %d, want 2", accepted)
	}

	entries, err := s.ListVocab(ctx, time.Time{})
	if err != nil {
		t.Fatalf("ListVocab: %v", err)
	}
	entry, ok := findEntry(entries, "word:ru:resilient")
	if !ok {
		t.Fatalf("aggregate missing; got %+v", entries)
	}
	if entry.Count != 2 {
		t.Errorf("Count = %d, want 2", entry.Count)
	}
	if entry.TargetLang != "ru" {
		t.Errorf("TargetLang = %q, want ru (derived from the key)", entry.TargetLang)
	}
	if !entry.FirstSeen.Equal(at.Add(-2 * time.Hour)) {
		t.Errorf("FirstSeen = %v, want %v", entry.FirstSeen, at.Add(-2*time.Hour))
	}
	if !entry.LastSeen.Equal(at) {
		t.Errorf("LastSeen = %v, want %v", entry.LastSeen, at)
	}
	// surface_forms are most-recent-first.
	if len(entry.SurfaceForms) != 2 || entry.SurfaceForms[0] != "resiliency" {
		t.Errorf("SurfaceForms = %v, want [resiliency resilient]", entry.SurfaceForms)
	}
	if entry.LatestCEFRLevel != model.CEFRB2 || entry.LatestTranslation != "устойчивый" {
		t.Errorf("latest_* not populated: %+v", entry)
	}
	if !entry.DeletedAt.IsZero() {
		t.Errorf("fresh entry has a tombstone: %v", entry.DeletedAt)
	}
}

func TestSaveLookups_IdempotentOnDuplicateID(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	ev := lookupAt("e1", "word:ru:resilient", "a1", 10, "resilient", at)

	if _, err := s.SaveLookups(ctx, []model.LookupEvent{ev}); err != nil {
		t.Fatalf("first SaveLookups: %v", err)
	}
	accepted, err := s.SaveLookups(ctx, []model.LookupEvent{ev})
	if err != nil {
		t.Fatalf("second SaveLookups: %v", err)
	}
	if accepted != 0 {
		t.Errorf("re-delivering the same event accepted %d, want 0", accepted)
	}

	entries, _ := s.ListVocab(ctx, time.Time{})
	entry, _ := findEntry(entries, "word:ru:resilient")
	if entry.Count != 1 {
		t.Errorf("Count = %d, want 1 — a retry must not inflate the counter", entry.Count)
	}
}

func TestSaveLookups_IdempotentOnDuplicatePosition(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)

	// Same (article, kind, span_start) reported twice under different ids —
	// e.g. by two devices. One occurrence, per WORD-CACHE-ARCH.md §3.2.
	if _, err := s.SaveLookups(ctx, []model.LookupEvent{
		lookupAt("e1", "word:ru:resilient", "a1", 10, "resilient", at),
	}); err != nil {
		t.Fatalf("first SaveLookups: %v", err)
	}
	accepted, err := s.SaveLookups(ctx, []model.LookupEvent{
		lookupAt("e2", "word:ru:resilient", "a1", 10, "resilient", at),
	})
	if err != nil {
		t.Fatalf("second SaveLookups: %v", err)
	}
	if accepted != 0 {
		t.Errorf("same position accepted %d, want 0", accepted)
	}

	entries, _ := s.ListVocab(ctx, time.Time{})
	entry, _ := findEntry(entries, "word:ru:resilient")
	if entry.Count != 1 {
		t.Errorf("Count = %d, want 1", entry.Count)
	}
}

func TestSaveLookups_FullyDuplicateBatchLeavesUpdatedAtAlone(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	ev := lookupAt("e1", "word:ru:resilient", "a1", 10, "resilient", at)

	if _, err := s.SaveLookups(ctx, []model.LookupEvent{ev}); err != nil {
		t.Fatalf("SaveLookups: %v", err)
	}
	before, _ := s.ListVocab(ctx, time.Time{})
	first, _ := findEntry(before, "word:ru:resilient")

	// The store stamps at second resolution, so wait past the boundary: if the
	// duplicate batch wrongly bumped updated_at, this test would see it.
	time.Sleep(1100 * time.Millisecond)
	if _, err := s.SaveLookups(ctx, []model.LookupEvent{ev}); err != nil {
		t.Fatalf("duplicate SaveLookups: %v", err)
	}
	after, _ := s.ListVocab(ctx, time.Time{})
	second, _ := findEntry(after, "word:ru:resilient")

	if !second.UpdatedAt.Equal(first.UpdatedAt) {
		t.Errorf("duplicate batch bumped UpdatedAt: %v → %v — the delta sync must stay quiet",
			first.UpdatedAt, second.UpdatedAt)
	}
}

func TestSaveLookups_SurfaceFormsAreCappedAndDeduped(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)

	var events []model.LookupEvent
	// 15 distinct forms, oldest first; only the newest MaxSurfaceForms survive.
	for i := range 15 {
		events = append(events, lookupAt(
			fmt.Sprintf("e%d", i), "word:ru:resilient", "a1", i,
			fmt.Sprintf("form%d", i), base.Add(time.Duration(i)*time.Minute)))
	}
	// A repeat of an existing form must not consume a second slot.
	events = append(events, lookupAt("dup", "word:ru:resilient", "a1", 99, "FORM14!",
		base.Add(20*time.Minute)))

	if _, err := s.SaveLookups(ctx, events); err != nil {
		t.Fatalf("SaveLookups: %v", err)
	}

	entries, _ := s.ListVocab(ctx, time.Time{})
	entry, _ := findEntry(entries, "word:ru:resilient")
	if len(entry.SurfaceForms) != model.MaxSurfaceForms {
		t.Fatalf("len(SurfaceForms) = %d, want %d", len(entry.SurfaceForms), model.MaxSurfaceForms)
	}
	if entry.SurfaceForms[0] != "form14" {
		t.Errorf("SurfaceForms[0] = %q, want the most recent form (normalized)", entry.SurfaceForms[0])
	}
	seen := map[string]bool{}
	for _, f := range entry.SurfaceForms {
		if seen[f] {
			t.Errorf("duplicate surface form %q in %v", f, entry.SurfaceForms)
		}
		seen[f] = true
	}
	if entry.Count != 16 {
		t.Errorf("Count = %d, want 16 (the cap applies to forms, not occurrences)", entry.Count)
	}
}

func TestListVocab_DeltaBySince(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)

	if _, err := s.SaveLookups(ctx, []model.LookupEvent{
		lookupAt("e1", "word:ru:old", "a1", 1, "old", at),
	}); err != nil {
		t.Fatalf("SaveLookups: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	cursor := time.Now().UTC().Truncate(time.Second)
	time.Sleep(1100 * time.Millisecond)

	if _, err := s.SaveLookups(ctx, []model.LookupEvent{
		lookupAt("e2", "word:ru:new", "a1", 2, "new", at),
	}); err != nil {
		t.Fatalf("SaveLookups: %v", err)
	}

	delta, err := s.ListVocab(ctx, cursor)
	if err != nil {
		t.Fatalf("ListVocab: %v", err)
	}
	if _, ok := findEntry(delta, "word:ru:new"); !ok {
		t.Errorf("delta missing the new entry: %+v", delta)
	}
	if _, ok := findEntry(delta, "word:ru:old"); ok {
		t.Errorf("delta wrongly includes the untouched entry: %+v", delta)
	}
}

func TestDeleteVocabEntry_TombstoneRidesTheDeltaAndRevives(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)

	if _, err := s.SaveLookups(ctx, []model.LookupEvent{
		lookupAt("e1", "word:ru:resilient", "a1", 10, "resilient", at),
	}); err != nil {
		t.Fatalf("SaveLookups: %v", err)
	}
	if err := s.DeleteVocabEntry(ctx, "word:ru:resilient"); err != nil {
		t.Fatalf("DeleteVocabEntry: %v", err)
	}

	// The tombstone is visible to ListVocab (so other devices apply the removal)…
	all, _ := s.ListVocab(ctx, time.Time{})
	entry, ok := findEntry(all, "word:ru:resilient")
	if !ok {
		t.Fatal("ListVocab dropped the tombstone; deletions must travel explicitly")
	}
	if entry.DeletedAt.IsZero() {
		t.Error("DeletedAt not stamped")
	}
	// …but never to ListKnownVocab.
	known, err := s.ListKnownVocab(ctx)
	if err != nil {
		t.Fatalf("ListKnownVocab: %v", err)
	}
	if _, ok := findEntry(known, "word:ru:resilient"); ok {
		t.Error("ListKnownVocab returned a tombstoned entry")
	}

	// A fresh lookup revives it: the user did not understand the word again.
	if _, err := s.SaveLookups(ctx, []model.LookupEvent{
		lookupAt("e2", "word:ru:resilient", "a2", 7, "resilient", at.Add(time.Minute)),
	}); err != nil {
		t.Fatalf("reviving SaveLookups: %v", err)
	}
	known, _ = s.ListKnownVocab(ctx)
	revived, ok := findEntry(known, "word:ru:resilient")
	if !ok {
		t.Fatal("a new lookup did not revive the deleted entry")
	}
	if !revived.DeletedAt.IsZero() {
		t.Errorf("revived entry still carries a tombstone: %v", revived.DeletedAt)
	}
	if revived.Count != 2 {
		t.Errorf("Count = %d, want 2 — the retained events still aggregate", revived.Count)
	}
}

func TestDeleteVocabEntry_UnknownKeyIsNoOp(t *testing.T) {
	s := openStore(t)
	if err := s.DeleteVocabEntry(context.Background(), "word:ru:nothing"); err != nil {
		t.Errorf("DeleteVocabEntry on unknown key: %v", err)
	}
}

func TestVocabularySurvivesArticleDeletion(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	a := makeArticle("https://example.com/vocab")
	if err := s.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	if _, err := s.SaveLookups(ctx, []model.LookupEvent{
		lookupAt("e1", "word:ru:resilient", a.ID, 10, "resilient", at),
	}); err != nil {
		t.Fatalf("SaveLookups: %v", err)
	}

	if err := s.DeleteArticle(ctx, a.ID); err != nil {
		t.Fatalf("DeleteArticle: %v", err)
	}

	entries, _ := s.ListVocab(ctx, time.Time{})
	entry, ok := findEntry(entries, "word:ru:resilient")
	if !ok {
		t.Fatal("deleting the article erased the vocabulary built from it")
	}
	if entry.LatestArticleTitle != "Test Article" {
		t.Errorf("denormalized title lost: %q", entry.LatestArticleTitle)
	}
}

func TestPruneVocabTombstones(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)

	if _, err := s.SaveLookups(ctx, []model.LookupEvent{
		lookupAt("e1", "word:ru:resilient", "a1", 10, "resilient", at),
	}); err != nil {
		t.Fatalf("SaveLookups: %v", err)
	}
	if err := s.DeleteVocabEntry(ctx, "word:ru:resilient"); err != nil {
		t.Fatalf("DeleteVocabEntry: %v", err)
	}

	// A fresh tombstone is inside the retention window and must survive.
	n, err := s.PruneVocabTombstones(ctx)
	if err != nil {
		t.Fatalf("PruneVocabTombstones: %v", err)
	}
	if n != 0 {
		t.Errorf("pruned %d fresh tombstones, want 0", n)
	}
	entries, _ := s.ListVocab(ctx, time.Time{})
	if _, ok := findEntry(entries, "word:ru:resilient"); !ok {
		t.Error("a fresh tombstone was pruned")
	}
}

// ── Lemma backfill ────────────────────────────────────────────────────────────

func TestLemmaBackfill_SelectsUnannotatedAndStampsVersion(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	a := makeArticle("https://example.com/backfill")
	a.Tokens = []model.Token{
		{Index: 0, Text: "The", Start: 0, End: 3},
		{Index: 1, Text: "children", Start: 4, End: 12},
	}
	if err := s.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}

	pending, err := s.ListArticlesForLemmaBackfill(ctx, 1, 20)
	if err != nil {
		t.Fatalf("ListArticlesForLemmaBackfill: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != a.ID {
		t.Fatalf("pending = %+v, want the freshly created article", pending)
	}

	annotated := append([]model.Token(nil), pending[0].Tokens...)
	annotated[1].Lemma = "child"
	if err := s.SaveTokenLemmas(ctx, a.ID, annotated, 1); err != nil {
		t.Fatalf("SaveTokenLemmas: %v", err)
	}

	// Stamped at the current version → no longer selected. A re-run is a no-op.
	pending, err = s.ListArticlesForLemmaBackfill(ctx, 1, 20)
	if err != nil {
		t.Fatalf("second ListArticlesForLemmaBackfill: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("article re-selected after stamping: %+v", pending)
	}

	// Bumping the generation re-selects it (dictionary upgrade).
	pending, err = s.ListArticlesForLemmaBackfill(ctx, 2, 20)
	if err != nil {
		t.Fatalf("third ListArticlesForLemmaBackfill: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("a higher version did not re-select the article: %+v", pending)
	}
}

func TestSaveTokenLemmas_PreservesIndicesAndBumpsUpdatedAt(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	a := makeArticle("https://example.com/indices")
	a.Tokens = []model.Token{
		{Index: 0, Text: "Mice", Start: 0, End: 4},
		{Index: 1, Text: "were", Start: 5, End: 9},
		{Index: 2, Text: "running", Start: 10, End: 17},
	}
	if err := s.CreateArticle(ctx, a); err != nil {
		t.Fatalf("CreateArticle: %v", err)
	}
	before, err := s.GetArticle(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetArticle: %v", err)
	}

	time.Sleep(1100 * time.Millisecond)
	annotated := append([]model.Token(nil), before.Tokens...)
	annotated[0].Lemma = "mouse"
	annotated[1].Lemma = "be"
	annotated[2].Lemma = "run"
	if err := s.SaveTokenLemmas(ctx, a.ID, annotated, 1); err != nil {
		t.Fatalf("SaveTokenLemmas: %v", err)
	}

	after, err := s.GetArticle(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetArticle after: %v", err)
	}
	if len(after.Tokens) != len(before.Tokens) {
		t.Fatalf("token count changed: %d → %d", len(before.Tokens), len(after.Tokens))
	}
	for i := range after.Tokens {
		b, aft := before.Tokens[i], after.Tokens[i]
		if aft.Index != b.Index || aft.Text != b.Text || aft.Start != b.Start || aft.End != b.End {
			t.Errorf("token %d changed beyond Lemma: %+v → %+v", i, b, aft)
		}
	}
	if after.Tokens[0].Lemma != "mouse" {
		t.Errorf("lemma not persisted: %+v", after.Tokens[0])
	}
	// The bump is what makes clients re-fetch the payload (isPayloadFresh).
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("UpdatedAt not bumped: %v → %v", before.UpdatedAt, after.UpdatedAt)
	}
}

func TestSaveTokenLemmas_UnknownArticle(t *testing.T) {
	s := openStore(t)
	err := s.SaveTokenLemmas(context.Background(), "nope", []model.Token{}, 1)
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("SaveTokenLemmas on unknown id = %v, want ErrNotFound", err)
	}
}

// Compile-time assertion that the concrete store still satisfies the port after
// the vocabulary additions.
var _ ports.Store = (*store.SQLite)(nil)
