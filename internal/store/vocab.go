// vocab.go implements the word-cache half of ports.Store: the append-only
// lookup_events log, the vocab_entries aggregate derived from it, and the
// article lemma backfill. See WORD-CACHE-ARCH.md §8.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"deep-reader/internal/model"
	"deep-reader/internal/ports"
	"deep-reader/internal/vocab"
)

// tombstoneRetention is how long a soft-deleted vocabulary entry is kept so the
// removal can reach every device through the delta sync. Past it the row is
// pruned on startup — a client that has been offline longer than this does a
// full sync anyway.
const tombstoneRetention = 90 * 24 * time.Hour

// SaveLookups records translation-lookup events and recomputes the affected
// vocabulary aggregates in one transaction. It is idempotent: an event whose id
// — or whose (ArticleID, Kind, SpanStart) position — is already stored is
// ignored. Recording an event for a previously deleted entry revives it.
//
// Aggregates are recomputed only for keys whose events actually landed, so a
// fully duplicate batch touches nothing and bumps no updated_at — which is what
// keeps the delta sync quiet under retries.
func (s *SQLite) SaveLookups(ctx context.Context, events []model.LookupEvent) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}

	s.wmu.Lock()
	defer s.wmu.Unlock()

	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: SaveLookups begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const insQ = `INSERT OR IGNORE INTO lookup_events
        (id, entry_key, kind, article_id, article_title, span_start, span_end,
         surface, lemma, translation, cefr_level, phrase_type, context,
         occurred_at, created_at)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

	createdAt := fmtTime(now())
	accepted := 0
	// touched keeps insertion order so the recomputation is deterministic.
	var touched []string
	seen := map[string]bool{}

	for _, e := range events {
		res, err := tx.ExecContext(ctx, insQ,
			e.ID, e.EntryKey, e.Kind, e.ArticleID, e.ArticleTitle, e.SpanStart, e.SpanEnd,
			e.Surface, e.Lemma, e.Translation, e.CEFRLevel, e.PhraseType, e.Context,
			fmtTime(e.OccurredAt), createdAt,
		)
		if err != nil {
			return 0, fmt.Errorf("store: SaveLookups insert: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("store: SaveLookups rows affected: %w", err)
		}
		if n == 0 {
			continue // duplicate id or duplicate position — a no-op by design
		}
		accepted++
		if !seen[e.EntryKey] {
			seen[e.EntryKey] = true
			touched = append(touched, e.EntryKey)
		}
	}

	stamp := now()
	for _, key := range touched {
		if err := recomputeAggregate(ctx, tx, key, stamp); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: SaveLookups commit: %w", err)
	}
	slog.Debug("store: lookups saved", "submitted", len(events), "accepted", accepted, "entries", len(touched))
	return accepted, nil
}

// recomputeAggregate rebuilds the vocab_entries row for entryKey from the
// events currently in the log, clearing any tombstone (a fresh lookup revives a
// deleted entry — see WORD-CACHE-ARCH.md §8.3).
func recomputeAggregate(ctx context.Context, tx *sql.Tx, entryKey string, stamp time.Time) error {
	var (
		count     int
		firstSeen string
		lastSeen  string
	)
	err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(MIN(occurred_at), ''), COALESCE(MAX(occurred_at), '')
         FROM lookup_events WHERE entry_key = ?`, entryKey,
	).Scan(&count, &firstSeen, &lastSeen)
	if err != nil {
		return fmt.Errorf("store: recomputeAggregate counts %q: %w", entryKey, err)
	}
	if count == 0 {
		// No events left for the key: nothing to aggregate.
		return nil
	}

	// The newest event supplies every latest_* field. id breaks ties
	// deterministically when two events share an occurred_at second.
	var latest model.LookupEvent
	err = tx.QueryRowContext(ctx,
		`SELECT kind, lemma, translation, cefr_level, phrase_type, context, article_id, article_title
         FROM lookup_events WHERE entry_key = ?
         ORDER BY occurred_at DESC, id DESC LIMIT 1`, entryKey,
	).Scan(&latest.Kind, &latest.Lemma, &latest.Translation, &latest.CEFRLevel,
		&latest.PhraseType, &latest.Context, &latest.ArticleID, &latest.ArticleTitle)
	if err != nil {
		return fmt.Errorf("store: recomputeAggregate latest %q: %w", entryKey, err)
	}

	forms, err := recentSurfaceForms(ctx, tx, entryKey)
	if err != nil {
		return err
	}
	formsJSON, err := json.Marshal(forms)
	if err != nil {
		return fmt.Errorf("store: recomputeAggregate marshal surface_forms: %w", err)
	}

	const upsertQ = `INSERT INTO vocab_entries
        (entry_key, kind, lemma, target_lang, surface_forms, count, first_seen, last_seen,
         latest_translation, latest_cefr_level, latest_phrase_type, latest_context,
         latest_article_id, latest_article_title, deleted_at, updated_at)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,'',?)
        ON CONFLICT(entry_key) DO UPDATE SET
            kind=excluded.kind,
            lemma=excluded.lemma,
            target_lang=excluded.target_lang,
            surface_forms=excluded.surface_forms,
            count=excluded.count,
            first_seen=excluded.first_seen,
            last_seen=excluded.last_seen,
            latest_translation=excluded.latest_translation,
            latest_cefr_level=excluded.latest_cefr_level,
            latest_phrase_type=excluded.latest_phrase_type,
            latest_context=excluded.latest_context,
            latest_article_id=excluded.latest_article_id,
            latest_article_title=excluded.latest_article_title,
            deleted_at='',
            updated_at=excluded.updated_at`

	_, err = tx.ExecContext(ctx, upsertQ,
		entryKey, latest.Kind, latest.Lemma, targetLangOf(entryKey), string(formsJSON), count,
		firstSeen, lastSeen, latest.Translation, latest.CEFRLevel, latest.PhraseType,
		latest.Context, latest.ArticleID, latest.ArticleTitle, fmtTime(stamp),
	)
	if err != nil {
		return fmt.Errorf("store: recomputeAggregate upsert %q: %w", entryKey, err)
	}
	return nil
}

// recentSurfaceForms returns up to model.MaxSurfaceForms distinct normalized
// surface forms observed for the entry, most recent first. They are the
// out-of-vocabulary matching fallback and the "seen as" display; the primary
// matching mechanism is the lemma (WORD-CACHE-ARCH.md §3.4).
func recentSurfaceForms(ctx context.Context, tx *sql.Tx, entryKey string) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT surface FROM lookup_events WHERE entry_key = ? AND surface != ''
         ORDER BY occurred_at DESC, id DESC`, entryKey)
	if err != nil {
		return nil, fmt.Errorf("store: recentSurfaceForms %q: %w", entryKey, err)
	}
	defer func() { _ = rows.Close() }()

	forms := []string{}
	seen := map[string]bool{}
	for rows.Next() {
		var surface string
		if err := rows.Scan(&surface); err != nil {
			return nil, fmt.Errorf("store: recentSurfaceForms scan: %w", err)
		}
		norm := vocab.Normalize(surface)
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		forms = append(forms, norm)
		if len(forms) == model.MaxSurfaceForms {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: recentSurfaceForms rows: %w", err)
	}
	return forms, nil
}

// targetLangOf extracts the target language from an entry key
// ("kind:lang:base"). It falls back to the default language for a malformed key
// rather than failing the write — validation happens at the API boundary.
func targetLangOf(entryKey string) string {
	parts := strings.SplitN(entryKey, ":", 3)
	if len(parts) < 3 || parts[1] == "" {
		return model.DefaultTargetLanguage
	}
	return parts[1]
}

// ListVocab returns vocabulary aggregates updated at or after `since`,
// tombstones included so clients can apply deletions. Pass the zero time for
// everything.
//
// As elsewhere the bound is inclusive (>=) so a row written in the same second
// the cursor was issued is not lost; the client's bulkPut makes the re-sent row
// a no-op.
func (s *SQLite) ListVocab(ctx context.Context, since time.Time) ([]model.VocabEntry, error) {
	return s.queryVocab(ctx, since, false)
}

// ListKnownVocab returns the live (non-tombstoned) aggregates used to steer
// enrichment: the terms the LLM should skip and the reader should hint from the
// dictionary. Kept separate from ListVocab so callers never filter tombstones.
func (s *SQLite) ListKnownVocab(ctx context.Context) ([]model.VocabEntry, error) {
	return s.queryVocab(ctx, time.Time{}, true)
}

const vocabColumns = `entry_key, kind, lemma, target_lang, surface_forms, count,
    first_seen, last_seen, latest_translation, latest_cefr_level, latest_phrase_type,
    latest_context, latest_article_id, latest_article_title, deleted_at, updated_at`

func (s *SQLite) queryVocab(ctx context.Context, since time.Time, aliveOnly bool) ([]model.VocabEntry, error) {
	var (
		where []string
		args  []any
	)
	if !since.IsZero() {
		where = append(where, "updated_at >= ?")
		args = append(args, fmtTime(since))
	}
	if aliveOnly {
		where = append(where, "deleted_at = ''")
	}

	q := `SELECT ` + vocabColumns + ` FROM vocab_entries`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY count DESC, entry_key ASC"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: ListVocab: %w", err)
	}
	defer func() { _ = rows.Close() }()

	entries := []model.VocabEntry{}
	for rows.Next() {
		entry, err := scanVocabEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: ListVocab rows: %w", err)
	}
	return entries, nil
}

func scanVocabEntry(rows *sql.Rows) (model.VocabEntry, error) {
	var (
		e                                            model.VocabEntry
		formsJSON, firstSeen, lastSeen, deleted, upd string
	)
	if err := rows.Scan(&e.EntryKey, &e.Kind, &e.Lemma, &e.TargetLang, &formsJSON, &e.Count,
		&firstSeen, &lastSeen, &e.LatestTranslation, &e.LatestCEFRLevel, &e.LatestPhraseType,
		&e.LatestContext, &e.LatestArticleID, &e.LatestArticleTitle, &deleted, &upd); err != nil {
		return model.VocabEntry{}, fmt.Errorf("store: scanVocabEntry: %w", err)
	}
	if err := json.Unmarshal([]byte(formsJSON), &e.SurfaceForms); err != nil {
		return model.VocabEntry{}, fmt.Errorf("store: scanVocabEntry surface_forms: %w", err)
	}
	if e.SurfaceForms == nil {
		e.SurfaceForms = []string{}
	}

	var err error
	if e.FirstSeen, err = parseTime(firstSeen); err != nil {
		return model.VocabEntry{}, err
	}
	if e.LastSeen, err = parseTime(lastSeen); err != nil {
		return model.VocabEntry{}, err
	}
	if e.DeletedAt, err = parseTime(deleted); err != nil {
		return model.VocabEntry{}, err
	}
	if e.UpdatedAt, err = parseTime(upd); err != nil {
		return model.VocabEntry{}, err
	}
	return e, nil
}

// DeleteVocabEntry soft-deletes the aggregate for entryKey by stamping
// deleted_at and bumping updated_at, so the removal rides the next delta sync.
// The underlying lookup_events are retained: the aggregate is rebuildable and a
// later lookup revives the entry, which is what makes deletion low-risk enough
// to need no confirmation dialog. It is a no-op for an unknown key.
func (s *SQLite) DeleteVocabEntry(ctx context.Context, entryKey string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	stamp := fmtTime(now())
	_, err := s.write.ExecContext(ctx,
		`UPDATE vocab_entries SET deleted_at = ?, updated_at = ? WHERE entry_key = ? AND deleted_at = ''`,
		stamp, stamp, entryKey)
	if err != nil {
		return fmt.Errorf("store: DeleteVocabEntry: %w", err)
	}
	return nil
}

// PruneVocabTombstones removes soft-deleted entries older than
// tombstoneRetention. It runs at startup next to the rest of store
// initialization and reports how many rows it dropped.
func (s *SQLite) PruneVocabTombstones(ctx context.Context) (int, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	cutoff := fmtTime(now().Add(-tombstoneRetention))
	res, err := s.write.ExecContext(ctx,
		`DELETE FROM vocab_entries WHERE deleted_at != '' AND deleted_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("store: PruneVocabTombstones: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: PruneVocabTombstones rows affected: %w", err)
	}
	return int(n), nil
}

// ── Lemma backfill ────────────────────────────────────────────────────────────

// ListArticlesForLemmaBackfill returns up to `limit` articles whose tokens were
// annotated by an older lemmatizer generation (lemma_version < version), oldest
// first, for the startup backfill (WORD-CACHE-ARCH.md §4.5).
func (s *SQLite) ListArticlesForLemmaBackfill(ctx context.Context, version, limit int) ([]model.Article, error) {
	// Same column list and order as ListWork, so scanArticleRow applies.
	const q = `SELECT id, source_url, url_hash, title, author, source_domain, lang,
                      original_text, content_format, tokens, summary, status, enrichment_version, error,
                      created_at, enriched_at, updated_at, pinned, llm_model
               FROM articles
               WHERE lemma_version < ? AND tokens != '' AND tokens != 'null'
               ORDER BY created_at ASC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, version, limit)
	if err != nil {
		return nil, fmt.Errorf("store: ListArticlesForLemmaBackfill: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var articles []model.Article
	for rows.Next() {
		a, err := scanArticleRow(rows)
		if err != nil {
			return nil, err
		}
		articles = append(articles, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: ListArticlesForLemmaBackfill rows: %w", err)
	}
	return articles, nil
}

// SaveTokenLemmas overwrites an article's stored tokens with `tokens` and stamps
// lemma_version, bumping updated_at so clients re-fetch the payload through the
// existing freshness check.
//
// `tokens` MUST be the existing slice with Lemma filled in — same length, same
// indices, same offsets — because every enrichment reference (token_index,
// start_index, end_index) indexes into it. The backfill never re-tokenizes; a
// shifted index would silently corrupt every annotation in the library.
func (s *SQLite) SaveTokenLemmas(ctx context.Context, id string, tokens []model.Token, version int) error {
	tokJSON, err := json.Marshal(tokens)
	if err != nil {
		return fmt.Errorf("store: SaveTokenLemmas marshal tokens: %w", err)
	}

	s.wmu.Lock()
	defer s.wmu.Unlock()

	res, err := s.write.ExecContext(ctx,
		`UPDATE articles SET tokens = ?, lemma_version = ?, updated_at = ? WHERE id = ?`,
		string(tokJSON), version, fmtTime(now()), id)
	if err != nil {
		return fmt.Errorf("store: SaveTokenLemmas: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: SaveTokenLemmas rows affected: %w", err)
	}
	if n == 0 {
		return ports.ErrNotFound
	}
	return nil
}
