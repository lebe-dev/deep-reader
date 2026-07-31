// handlers_vocab.go serves the write side of the word cache. The read side is
// the /api/config delta (see getConfig), which is a GET, so the writes need
// their own routes. See WORD-CACHE-ARCH.md §7.1 and §8.4.
package api

import (
	"github.com/gofiber/fiber/v3"

	"deep-reader/internal/model"
)

// Length caps for a lookup event. They exist to bound what a buggy or hostile
// client can push into the log, not to enforce meaning — the fields are free
// text taken from the article and the model's answer.
const (
	maxSurfaceLen     = 200
	maxLemmaLen       = 200
	maxTranslationLen = 500
	maxContextLen     = 500
	maxEntryKeyLen    = 300
	maxArticleIDLen   = 100
	maxTitleLen       = 500
)

// saveLookups records a batch of translation lookups.
//
// POST /api/lookups  {events: [...]}  ->  {accepted: n}
//
// It is fully idempotent: a repeated event id, or a repeat of the same
// (article_id, kind, span_start) position, is ignored, so the client can retry a
// batch freely. `accepted` reports how many rows actually landed.
func (s *Server) saveLookups(c fiber.Ctx) error {
	var req model.SaveLookupsRequest
	if err := c.Bind().Body(&req); err != nil {
		return sendError(c, fiber.StatusBadRequest, "invalid JSON body")
	}
	if len(req.Events) == 0 {
		return c.JSON(model.SaveLookupsResponse{Accepted: 0})
	}
	if len(req.Events) > model.MaxLookupBatch {
		return sendError(c, fiber.StatusRequestEntityTooLarge, "too many events in one request")
	}
	// An invalid event fails the whole batch: silently dropping one would leave
	// the client believing a lookup was recorded when it was not.
	for i := range req.Events {
		if msg, ok := validateLookupEvent(req.Events[i]); !ok {
			return sendError(c, fiber.StatusBadRequest, msg)
		}
	}

	accepted, err := s.store.SaveLookups(c.Context(), req.Events)
	if err != nil {
		return s.serverError(c, "save lookups", err)
	}
	return c.JSON(model.SaveLookupsResponse{Accepted: accepted})
}

// deleteVocabEntry soft-deletes one vocabulary aggregate.
//
// POST /api/vocab/delete  {entry_key}  ->  204
//
// The key travels in the body, not the path: it contains spaces and arbitrary
// punctuation (phrases), and percent-encoding that into a path segment is a
// needless escaping-bug surface. Deleting an unknown key is a no-op, so the
// client can retry.
func (s *Server) deleteVocabEntry(c fiber.Ctx) error {
	var req model.DeleteVocabRequest
	if err := c.Bind().Body(&req); err != nil {
		return sendError(c, fiber.StatusBadRequest, "invalid JSON body")
	}
	if req.EntryKey == "" {
		return sendError(c, fiber.StatusBadRequest, "entry_key must not be empty")
	}
	if len(req.EntryKey) > maxEntryKeyLen {
		return sendError(c, fiber.StatusBadRequest, "entry_key is too long")
	}

	if err := s.store.DeleteVocabEntry(c.Context(), req.EntryKey); err != nil {
		return s.serverError(c, "delete vocab entry", err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// validateLookupEvent checks one event against the wire contract, returning a
// human-readable message and false on the first problem.
func validateLookupEvent(e model.LookupEvent) (msg string, ok bool) {
	if e.ID == "" {
		return "event id must not be empty", false
	}
	if e.EntryKey == "" {
		return "entry_key must not be empty", false
	}
	if len(e.EntryKey) > maxEntryKeyLen {
		return "entry_key is too long", false
	}
	if e.Kind != model.LookupKindWord && e.Kind != model.LookupKindPhrase {
		return "kind must be one of word, phrase", false
	}
	if e.SpanStart < 0 {
		return "span_start must not be negative", false
	}
	if e.SpanEnd < e.SpanStart {
		return "span_end must not be before span_start", false
	}
	if len(e.Surface) > maxSurfaceLen {
		return "surface is too long", false
	}
	if len(e.Lemma) > maxLemmaLen {
		return "lemma is too long", false
	}
	if len(e.Translation) > maxTranslationLen {
		return "translation is too long", false
	}
	if len(e.Context) > maxContextLen {
		return "context is too long", false
	}
	if len(e.ArticleID) > maxArticleIDLen {
		return "article_id is too long", false
	}
	if len(e.ArticleTitle) > maxTitleLen {
		return "article_title is too long", false
	}
	if e.OccurredAt.IsZero() {
		return "occurred_at must be set", false
	}
	return "", true
}
