package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"deep-reader/internal/model"
)

func validEvent() model.LookupEvent {
	return model.LookupEvent{
		ID:           "11111111-2222-3333-4444-555555555555",
		EntryKey:     "word:ru:resilient",
		Kind:         model.LookupKindWord,
		ArticleID:    "a1",
		ArticleTitle: "The Economist",
		SpanStart:    42,
		SpanEnd:      42,
		Surface:      "resilient",
		Lemma:        "resilient",
		Translation:  "устойчивый",
		CEFRLevel:    model.CEFRB2,
		Context:      "proved remarkably resilient to shocks",
		OccurredAt:   time.Now().UTC(),
	}
}

func TestSaveLookups_StoresBatch(t *testing.T) {
	st := &fakeStore{}
	s := newTestServer(t, st, &fakeIngestor{})

	body := model.SaveLookupsRequest{Events: []model.LookupEvent{validEvent()}}
	resp := doReq(t, s, http.MethodPost, "/api/lookups", body, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got model.SaveLookupsResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Accepted != 1 {
		t.Errorf("accepted = %d, want 1", got.Accepted)
	}
	if len(st.savedLookups) != 1 || st.savedLookups[0].EntryKey != "word:ru:resilient" {
		t.Errorf("store received %+v", st.savedLookups)
	}
}

func TestSaveLookups_EmptyBatchIsAccepted(t *testing.T) {
	st := &fakeStore{}
	s := newTestServer(t, st, &fakeIngestor{})

	resp := doReq(t, s, http.MethodPost, "/api/lookups",
		model.SaveLookupsRequest{Events: []model.LookupEvent{}}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(st.savedLookups) != 0 {
		t.Errorf("store was called for an empty batch: %+v", st.savedLookups)
	}
}

func TestSaveLookups_RejectsOversizedBatch(t *testing.T) {
	st := &fakeStore{}
	s := newTestServer(t, st, &fakeIngestor{})

	events := make([]model.LookupEvent, model.MaxLookupBatch+1)
	for i := range events {
		e := validEvent()
		e.ID = string(rune('a'+i%26)) + strings.Repeat("x", i%5+1)
		e.SpanStart = i
		e.SpanEnd = i
		events[i] = e
	}

	resp := doReq(t, s, http.MethodPost, "/api/lookups",
		model.SaveLookupsRequest{Events: events}, testToken)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
	if len(st.savedLookups) != 0 {
		t.Error("an oversized batch reached the store")
	}
}

func TestSaveLookups_ValidationFailsWholeBatch(t *testing.T) {
	cases := []struct {
		name  string
		mutan func(*model.LookupEvent)
	}{
		{"empty id", func(e *model.LookupEvent) { e.ID = "" }},
		{"empty entry_key", func(e *model.LookupEvent) { e.EntryKey = "" }},
		{"overlong entry_key", func(e *model.LookupEvent) { e.EntryKey = strings.Repeat("k", 301) }},
		{"unknown kind", func(e *model.LookupEvent) { e.Kind = "sentence" }},
		{"negative span_start", func(e *model.LookupEvent) { e.SpanStart = -1 }},
		{"inverted span", func(e *model.LookupEvent) { e.SpanStart = 5; e.SpanEnd = 4 }},
		{"overlong surface", func(e *model.LookupEvent) { e.Surface = strings.Repeat("s", 201) }},
		{"overlong translation", func(e *model.LookupEvent) { e.Translation = strings.Repeat("t", 501) }},
		{"overlong context", func(e *model.LookupEvent) { e.Context = strings.Repeat("c", 501) }},
		{"zero occurred_at", func(e *model.LookupEvent) { e.OccurredAt = time.Time{} }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeStore{}
			s := newTestServer(t, st, &fakeIngestor{})

			bad := validEvent()
			tc.mutan(&bad)
			good := validEvent()
			good.ID = "good"
			good.SpanStart = 7
			good.SpanEnd = 7

			resp := doReq(t, s, http.MethodPost, "/api/lookups",
				model.SaveLookupsRequest{Events: []model.LookupEvent{good, bad}}, testToken)
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.StatusCode)
			}
			// The valid sibling must not land either: a partial write would let
			// the client believe the rejected lookup was recorded.
			if len(st.savedLookups) != 0 {
				t.Errorf("a rejected batch partially reached the store: %+v", st.savedLookups)
			}
		})
	}
}

func TestSaveLookups_RequiresAuth(t *testing.T) {
	st := &fakeStore{}
	s := newTestServer(t, st, &fakeIngestor{})

	resp := doReq(t, s, http.MethodPost, "/api/lookups",
		model.SaveLookupsRequest{Events: []model.LookupEvent{validEvent()}}, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if len(st.savedLookups) != 0 {
		t.Error("an unauthenticated lookup reached the store")
	}
}

func TestSaveLookups_StoreFailureIs500(t *testing.T) {
	st := &fakeStore{saveLookupsErr: errors.New("disk on fire")}
	s := newTestServer(t, st, &fakeIngestor{})

	resp := doReq(t, s, http.MethodPost, "/api/lookups",
		model.SaveLookupsRequest{Events: []model.LookupEvent{validEvent()}}, testToken)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestDeleteVocabEntry(t *testing.T) {
	st := &fakeStore{}
	s := newTestServer(t, st, &fakeIngestor{})

	resp := doReq(t, s, http.MethodPost, "/api/vocab/delete",
		model.DeleteVocabRequest{EntryKey: "phrase:ru:spill the bean"}, testToken)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if len(st.deletedVocabKeys) != 1 || st.deletedVocabKeys[0] != "phrase:ru:spill the bean" {
		t.Errorf("store received %v", st.deletedVocabKeys)
	}
}

func TestDeleteVocabEntry_RejectsEmptyKey(t *testing.T) {
	st := &fakeStore{}
	s := newTestServer(t, st, &fakeIngestor{})

	resp := doReq(t, s, http.MethodPost, "/api/vocab/delete",
		model.DeleteVocabRequest{EntryKey: ""}, testToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if len(st.deletedVocabKeys) != 0 {
		t.Error("an empty key reached the store")
	}
}

func TestDeleteVocabEntry_RequiresAuth(t *testing.T) {
	st := &fakeStore{}
	s := newTestServer(t, st, &fakeIngestor{})

	resp := doReq(t, s, http.MethodPost, "/api/vocab/delete",
		model.DeleteVocabRequest{EntryKey: "word:ru:resilient"}, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if len(st.deletedVocabKeys) != 0 {
		t.Error("an unauthenticated delete reached the store")
	}
}

func TestGetConfig_CarriesVocabDelta(t *testing.T) {
	when := time.Now().UTC().Truncate(time.Second)
	st := &fakeStore{
		vocab: []model.VocabEntry{
			{EntryKey: "word:ru:resilient", Kind: model.LookupKindWord, Lemma: "resilient",
				TargetLang: "ru", SurfaceForms: []string{"resilient"}, Count: 3, UpdatedAt: when},
			// A tombstone must travel too: deletions are never inferred from absence.
			{EntryKey: "word:ru:gone", Kind: model.LookupKindWord, Lemma: "gone",
				TargetLang: "ru", DeletedAt: when, UpdatedAt: when},
		},
	}
	s := newTestServer(t, st, &fakeIngestor{})

	since := when.Add(-time.Hour).Format(time.RFC3339)
	resp := doReq(t, s, http.MethodGet, "/api/config?since="+since, nil, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got model.ConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Vocab) != 2 {
		t.Fatalf("vocab has %d entries, want 2: %+v", len(got.Vocab), got.Vocab)
	}
	if got.Vocab[1].DeletedAt.IsZero() {
		t.Error("the tombstone lost its deleted_at on the wire")
	}
	// The vocabulary shares the existing cursor rather than owning a second one.
	if !st.vocabSince.Equal(when.Add(-time.Hour)) {
		t.Errorf("ListVocab called with since=%v, want the request cursor %v",
			st.vocabSince, when.Add(-time.Hour))
	}
}

func TestGetConfig_UnauthenticatedLeaksNoVocab(t *testing.T) {
	st := &fakeStore{
		vocab: []model.VocabEntry{{EntryKey: "word:ru:secret", Lemma: "secret"}},
	}
	s := newTestServer(t, st, &fakeIngestor{})

	resp := doReq(t, s, http.MethodGet, "/api/config", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got model.ConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Vocab) != 0 {
		t.Errorf("unauthenticated /api/config leaked the vocabulary: %+v", got.Vocab)
	}
}

func TestPatchSettings_VocabAssist(t *testing.T) {
	var applied model.SettingsPatch
	st := &fakeStore{
		updateSettings: func(p model.SettingsPatch) (model.Settings, error) {
			applied = p
			return model.Settings{VocabAssist: *p.VocabAssist}, nil
		},
	}
	s := newTestServer(t, st, &fakeIngestor{})

	resp := doReq(t, s, http.MethodPatch, "/api/settings",
		map[string]any{"vocab_assist": false}, testToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if applied.VocabAssist == nil || *applied.VocabAssist {
		t.Errorf("vocab_assist patch not applied: %+v", applied.VocabAssist)
	}
}
