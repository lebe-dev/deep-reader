// Package store implements the persistent storage layer for Deep Reader using
// SQLite (modernc.org/sqlite — pure Go, CGo-free). It exposes [SQLite], which
// satisfies [ports.Store].
//
// Connection model: one dedicated write connection with WAL mode and a read
// pool (sql.DB default pool), separated to avoid "database is locked" errors
// under concurrent read + write access.
//
// Migrations are applied on open via goose with an embedded FS so the binary
// is self-contained — no external SQL files required at runtime.
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/pressly/goose/v3"

	"deep-reader/internal/config"
	"deep-reader/internal/model"
	"deep-reader/internal/ports"

	_ "modernc.org/sqlite" // register "sqlite" driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// SQLite is the concrete store implementation backed by a SQLite database file.
// It satisfies ports.Store. Use [NewSQLite] to construct it.
type SQLite struct {
	// db is used for all reads (pool of connections).
	db *sql.DB
	// wmu serialises writes through a single connection to avoid SQLite's
	// exclusive-write constraint.
	wmu sync.Mutex
	// write is a single dedicated write connection.
	write *sql.DB
}

// NewSQLite opens (or creates) the SQLite database at cfg.DatabasePath, applies
// goose migrations from the embedded FS, and returns a ready-to-use *SQLite.
func NewSQLite(ctx context.Context, cfg *config.Config) (*SQLite, error) {
	// modernc.org/sqlite uses _pragma=<name>(<value>) syntax (not _foreign_keys=ON).
	dsn := cfg.DatabasePath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"

	// Read pool — may use multiple connections (SQLite WAL allows concurrent readers).
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open read pool: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping read pool: %w", err)
	}

	// Write connection — single writer.
	wdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: open write conn: %w", err)
	}
	wdb.SetMaxOpenConns(1)
	wdb.SetMaxIdleConns(1)
	if err := wdb.PingContext(ctx); err != nil {
		_ = db.Close()
		_ = wdb.Close()
		return nil, fmt.Errorf("store: ping write conn: %w", err)
	}

	// Run goose migrations from the embedded FS.
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		_ = db.Close()
		_ = wdb.Close()
		return nil, fmt.Errorf("store: goose set dialect: %w", err)
	}
	if err := goose.Up(wdb, "migrations"); err != nil {
		_ = db.Close()
		_ = wdb.Close()
		return nil, fmt.Errorf("store: goose up: %w", err)
	}

	version, verErr := goose.GetDBVersion(wdb)
	if verErr != nil {
		slog.Debug("store: could not read migration version", "err", verErr)
	}
	slog.Info("store: database opened", "path", cfg.DatabasePath, "schema_version", version)

	s := &SQLite{db: db, write: wdb}

	// Seed the active LLM provider from the LLM_* env vars on first boot, so
	// env-configured deployments keep working once the UI becomes the source of
	// truth. No-op once any profile exists.
	if err := s.seedLLMProvider(ctx, cfg); err != nil {
		_ = db.Close()
		_ = wdb.Close()
		return nil, err
	}

	return s, nil
}

// Close releases all database connections.
func (s *SQLite) Close() error {
	err1 := s.db.Close()
	err2 := s.write.Close()
	return errors.Join(err1, err2)
}

// newID generates a new ULID as a string.
func newID() string {
	return ulid.Make().String()
}

// now returns the current UTC time truncated to seconds for consistent storage.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Second)
}

// fmtTime formats a time.Time to the RFC3339 TEXT form stored in the DB.
// Zero time is stored as empty string.
func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// parseTime parses an RFC3339 TEXT value from the DB. Empty string returns
// zero time without error.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("store: parseTime %q: %w", s, err)
	}
	return t, nil
}

// ── Settings ──────────────────────────────────────────────────────────────────

// GetSettings returns the singleton settings row. The row is guaranteed to
// exist because the migration seeds it; if somehow absent, a default is returned.
func (s *SQLite) GetSettings(ctx context.Context) (model.Settings, error) {
	const q = `SELECT cefr_level, target_language, llm_model, min_difficulty_to_highlight, markdown_warn_threshold, enrichment_prompt, summary_prompt, normalize_prompt, bot_wall_signatures, chunk_tokens, font_size, line_height, updated_at
               FROM settings WHERE id = 1`
	row := s.db.QueryRowContext(ctx, q)
	return scanSettings(row)
}

// UpdateSettings applies the patch fields (nil = unchanged), stamps updated_at,
// and returns the resulting settings row.
func (s *SQLite) UpdateSettings(ctx context.Context, patch model.SettingsPatch) (model.Settings, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	// Read current values first.
	const selQ = `SELECT cefr_level, target_language, llm_model, min_difficulty_to_highlight, markdown_warn_threshold, enrichment_prompt, summary_prompt, normalize_prompt, bot_wall_signatures, chunk_tokens, font_size, line_height, updated_at
                  FROM settings WHERE id = 1`
	row := s.write.QueryRowContext(ctx, selQ)
	cur, err := scanSettings(row)
	if err != nil {
		return model.Settings{}, fmt.Errorf("store: UpdateSettings read: %w", err)
	}

	// Apply patch.
	if patch.CEFRLevel != nil {
		cur.CEFRLevel = *patch.CEFRLevel
	}
	if patch.TargetLanguage != nil {
		cur.TargetLanguage = *patch.TargetLanguage
	}
	if patch.LLMModel != nil {
		cur.LLMModel = *patch.LLMModel
	}
	if patch.MinDifficultyToHighlight != nil {
		cur.MinDifficultyToHighlight = *patch.MinDifficultyToHighlight
	}
	if patch.MarkdownWarnThreshold != nil {
		cur.MarkdownWarnThreshold = *patch.MarkdownWarnThreshold
	}
	if patch.EnrichmentPrompt != nil {
		cur.EnrichmentPrompt = *patch.EnrichmentPrompt
	}
	if patch.SummaryPrompt != nil {
		cur.SummaryPrompt = *patch.SummaryPrompt
	}
	if patch.NormalizePrompt != nil {
		cur.NormalizePrompt = *patch.NormalizePrompt
	}
	if patch.BotWallSignatures != nil {
		cur.BotWallSignatures = *patch.BotWallSignatures
	}
	if patch.ChunkTokens != nil {
		cur.ChunkTokens = *patch.ChunkTokens
	}
	if patch.FontSize != nil {
		cur.FontSize = *patch.FontSize
	}
	if patch.LineHeight != nil {
		cur.LineHeight = *patch.LineHeight
	}
	cur.UpdatedAt = now()

	const updQ = `UPDATE settings SET cefr_level=?, target_language=?, llm_model=?,
                  min_difficulty_to_highlight=?, markdown_warn_threshold=?, enrichment_prompt=?, summary_prompt=?, normalize_prompt=?, bot_wall_signatures=?, chunk_tokens=?, font_size=?, line_height=?, updated_at=? WHERE id = 1`
	if _, err := s.write.ExecContext(ctx, updQ,
		cur.CEFRLevel, cur.TargetLanguage, cur.LLMModel,
		cur.MinDifficultyToHighlight, cur.MarkdownWarnThreshold, cur.EnrichmentPrompt, cur.SummaryPrompt, cur.NormalizePrompt, cur.BotWallSignatures, cur.ChunkTokens, cur.FontSize, cur.LineHeight, fmtTime(cur.UpdatedAt),
	); err != nil {
		return model.Settings{}, fmt.Errorf("store: UpdateSettings write: %w", err)
	}
	slog.Debug("store: settings updated",
		"cefr_level", cur.CEFRLevel,
		"target_language", cur.TargetLanguage,
		"llm_model", cur.LLMModel,
		"min_difficulty_to_highlight", cur.MinDifficultyToHighlight,
		"markdown_warn_threshold", cur.MarkdownWarnThreshold,
		"enrichment_prompt_bytes", len(cur.EnrichmentPrompt),
	)
	return cur, nil
}

func scanSettings(row *sql.Row) (model.Settings, error) {
	var s model.Settings
	var updatedAtStr string
	if err := row.Scan(&s.CEFRLevel, &s.TargetLanguage, &s.LLMModel,
		&s.MinDifficultyToHighlight, &s.MarkdownWarnThreshold, &s.EnrichmentPrompt, &s.SummaryPrompt, &s.NormalizePrompt, &s.BotWallSignatures, &s.ChunkTokens, &s.FontSize, &s.LineHeight, &updatedAtStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Return defaults if somehow the singleton row is missing.
			return model.Settings{
				CEFRLevel:                model.CEFRA2,
				TargetLanguage:           model.DefaultTargetLanguage,
				MinDifficultyToHighlight: model.CEFRB1,
				MarkdownWarnThreshold:    model.DefaultMarkdownWarnThreshold,
				FontSize:                 model.DefaultFontSize,
				LineHeight:               model.DefaultLineHeight,
			}, nil
		}
		return model.Settings{}, fmt.Errorf("store: scanSettings: %w", err)
	}
	t, err := parseTime(updatedAtStr)
	if err != nil {
		return model.Settings{}, err
	}
	s.UpdatedAt = t
	return s, nil
}

// ── Articles ──────────────────────────────────────────────────────────────────

// CreateArticle inserts a new article. Returns [ports.ErrDuplicate] when the
// url_hash already exists (UNIQUE constraint violation).
func (s *SQLite) CreateArticle(ctx context.Context, a *model.Article) error {
	tokJSON, err := json.Marshal(a.Tokens)
	if err != nil {
		return fmt.Errorf("store: CreateArticle marshal tokens: %w", err)
	}

	s.wmu.Lock()
	defer s.wmu.Unlock()

	const q = `INSERT INTO articles
               (id, source_url, url_hash, title, author, source_domain, lang,
                original_text, tokens, status, enrichment_version, error,
                created_at, enriched_at, updated_at)
               VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	_, err = s.write.ExecContext(ctx, q,
		a.ID, a.SourceURL, a.URLHash, a.Title, a.Author, a.SourceDomain, a.Lang,
		a.OriginalText, string(tokJSON), a.Status, a.EnrichmentVersion, a.Error,
		fmtTime(a.CreatedAt), fmtTime(a.EnrichedAt), fmtTime(a.UpdatedAt),
	)
	if err != nil {
		if isSQLiteUnique(err) {
			return ports.ErrDuplicate
		}
		return fmt.Errorf("store: CreateArticle: %w", err)
	}
	slog.Debug("store: article created", "article_id", a.ID, "url_hash", a.URLHash, "status", a.Status)
	return nil
}

// GetArticleByHash returns the article with the given url_hash, or
// [ports.ErrNotFound] if it does not exist.
func (s *SQLite) GetArticleByHash(ctx context.Context, urlHash string) (*model.Article, error) {
	const q = `SELECT id, source_url, url_hash, title, author, source_domain, lang,
                      original_text, tokens, status, enrichment_version, error,
                      created_at, enriched_at, updated_at, pinned
               FROM articles WHERE url_hash = ?`
	row := s.db.QueryRowContext(ctx, q, urlHash)
	a, err := scanArticle(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ports.ErrNotFound
		}
		return nil, err
	}
	return a, nil
}

// ListArticleMeta returns library metadata for all articles whose updated_at is
// >= since. Pass the zero time to list everything.
func (s *SQLite) ListArticleMeta(ctx context.Context, since time.Time) ([]model.ArticleMeta, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if since.IsZero() {
		const q = `SELECT id, source_url, title, author, source_domain, status, pinned, created_at, enriched_at, enrichment_version,
                          json_array_length(tokens), enrichment_coverage, COALESCE(summary, '')
                   FROM articles ORDER BY created_at DESC`
		rows, err = s.db.QueryContext(ctx, q)
	} else {
		const q = `SELECT id, source_url, title, author, source_domain, status, pinned, created_at, enriched_at, enrichment_version,
                          json_array_length(tokens), enrichment_coverage, COALESCE(summary, '')
                   FROM articles WHERE updated_at > ? ORDER BY created_at DESC`
		rows, err = s.db.QueryContext(ctx, q, fmtTime(since))
	}
	if err != nil {
		return nil, fmt.Errorf("store: ListArticleMeta: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var metas []model.ArticleMeta
	for rows.Next() {
		var m model.ArticleMeta
		var createdAtStr, enrichedAtStr string
		var pinned int
		if err := rows.Scan(&m.ID, &m.SourceURL, &m.Title, &m.Author, &m.SourceDomain,
			&m.Status, &pinned, &createdAtStr, &enrichedAtStr, &m.EnrichmentVersion, &m.TokenCount,
			&m.EnrichmentCoverage, &m.Summary); err != nil {
			return nil, fmt.Errorf("store: ListArticleMeta scan: %w", err)
		}
		m.Pinned = pinned == 1
		if m.CreatedAt, err = parseTime(createdAtStr); err != nil {
			return nil, err
		}
		if m.EnrichedAt, err = parseTime(enrichedAtStr); err != nil {
			return nil, err
		}
		metas = append(metas, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: ListArticleMeta rows: %w", err)
	}
	if metas == nil {
		metas = []model.ArticleMeta{}
	}
	return metas, nil
}

// GetArticle returns the full server-side article record, or [ports.ErrNotFound].
func (s *SQLite) GetArticle(ctx context.Context, id string) (*model.Article, error) {
	const q = `SELECT id, source_url, url_hash, title, author, source_domain, lang,
                      original_text, tokens, status, enrichment_version, error,
                      created_at, enriched_at, updated_at, pinned
               FROM articles WHERE id = ?`
	row := s.db.QueryRowContext(ctx, q, id)
	a, err := scanArticle(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ports.ErrNotFound
		}
		return nil, err
	}
	return a, nil
}

// GetArticlePayload returns the client-facing payload (tokens + enrichment).
// Enrichment is nil when the article has no enrichment row.
func (s *SQLite) GetArticlePayload(ctx context.Context, id string) (*model.ArticlePayload, error) {
	const q = `SELECT a.id, a.title, a.author, a.lang, a.original_text, a.tokens,
                      a.summary, a.status, a.enrichment_version, a.enrichment_coverage, e.enrichment
               FROM articles a
               LEFT JOIN enrichments e ON e.article_id = a.id
               WHERE a.id = ?`
	row := s.db.QueryRowContext(ctx, q, id)

	var p model.ArticlePayload
	var tokJSON string
	var enrichJSON sql.NullString
	if err := row.Scan(&p.ID, &p.Title, &p.Author, &p.Lang, &p.OriginalText,
		&tokJSON, &p.Summary, &p.Status, &p.EnrichmentVersion, &p.EnrichmentCoverage, &enrichJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ports.ErrNotFound
		}
		return nil, fmt.Errorf("store: GetArticlePayload scan: %w", err)
	}

	if err := json.Unmarshal([]byte(tokJSON), &p.Tokens); err != nil {
		return nil, fmt.Errorf("store: GetArticlePayload unmarshal tokens: %w", err)
	}
	if p.Tokens == nil {
		p.Tokens = []model.Token{}
	}

	if enrichJSON.Valid && enrichJSON.String != "" && enrichJSON.String != "{}" {
		var e model.Enrichment
		if err := json.Unmarshal([]byte(enrichJSON.String), &e); err != nil {
			return nil, fmt.Errorf("store: GetArticlePayload unmarshal enrichment: %w", err)
		}
		p.Enrichment = &e
	}
	return &p, nil
}

// DeleteArticle removes the article by id (cascading to enrichments and
// progress). Returns [ports.ErrNotFound] if it did not exist.
func (s *SQLite) DeleteArticle(ctx context.Context, id string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	res, err := s.write.ExecContext(ctx, `DELETE FROM articles WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: DeleteArticle: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ports.ErrNotFound
	}
	slog.Debug("store: article deleted", "article_id", id)
	return nil
}

// SetStatus updates an article's status and error text, stamping updated_at.
// It also clears any previously captured raw LLM response — a plain status flip
// (e.g. into an in-flight state) starts a fresh attempt; SetFailed is the path
// that stores a raw response.
func (s *SQLite) SetStatus(ctx context.Context, id, status, errMsg string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	const q = `UPDATE articles SET status=?, error=?, raw_llm_response='', updated_at=? WHERE id=?`
	res, err := s.write.ExecContext(ctx, q, status, errMsg, fmtTime(now()), id)
	if err != nil {
		return fmt.Errorf("store: SetStatus: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ports.ErrNotFound
	}
	slog.Debug("store: article status updated", "article_id", id, "status", status)
	return nil
}

// SetFailed records a terminal stage failure, storing the error message and the
// raw LLM response (when available) for inspection, and stamps updated_at.
func (s *SQLite) SetFailed(ctx context.Context, id, status, errMsg, rawLLMResponse string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	const q = `UPDATE articles SET status=?, error=?, raw_llm_response=?, updated_at=? WHERE id=?`
	res, err := s.write.ExecContext(ctx, q, status, errMsg, rawLLMResponse, fmtTime(now()), id)
	if err != nil {
		return fmt.Errorf("store: SetFailed: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ports.ErrNotFound
	}
	slog.Debug("store: article failed status set", "article_id", id, "status", status, "raw_bytes", len(rawLLMResponse))
	return nil
}

// GetArticleRaw returns the raw LLM response (with error/status) captured for an
// article, or [ports.ErrNotFound]. Raw is empty when nothing was captured.
func (s *SQLite) GetArticleRaw(ctx context.Context, id string) (*model.ArticleRaw, error) {
	const q = `SELECT id, status, error, raw_llm_response FROM articles WHERE id = ?`
	var r model.ArticleRaw
	if err := s.db.QueryRowContext(ctx, q, id).Scan(&r.ID, &r.Status, &r.Error, &r.Raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ports.ErrNotFound
		}
		return nil, fmt.Errorf("store: GetArticleRaw: %w", err)
	}
	return &r, nil
}

// sentenceCoverage returns the fraction [0,1] of tokens in [0,tokenCount) that
// fall within at least one sentence range in e. It is the completeness signal
// shown in the UI: a low value means the LLM stopped translating partway and
// left the tail of the article unannotated. Returns 0 when tokenCount is 0.
func sentenceCoverage(e model.Enrichment, tokenCount int) float64 {
	if tokenCount <= 0 {
		return 0
	}
	covered := make([]bool, tokenCount)
	n := 0
	for _, sentence := range e.Sentences {
		start := max(sentence.StartIndex, 0)
		end := min(sentence.EndIndex, tokenCount-1)
		for i := start; i <= end; i++ {
			if !covered[i] {
				covered[i] = true
				n++
			}
		}
	}
	return float64(n) / float64(tokenCount)
}

// SaveEnrichment persists the enrichment blob, sets status=enriched, records
// enriched_at, computes the sentence-coverage completeness signal, and stamps
// updated_at. The operation is atomic.
func (s *SQLite) SaveEnrichment(ctx context.Context, id string, e model.Enrichment, enrichedAt time.Time) error {
	eJSON, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("store: SaveEnrichment marshal: %w", err)
	}

	s.wmu.Lock()
	defer s.wmu.Unlock()

	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: SaveEnrichment begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Read the token count in-tx to compute coverage against the article the
	// enrichment belongs to. A missing row means the article was deleted.
	var tokenCount int
	if err := tx.QueryRowContext(ctx, `SELECT json_array_length(tokens) FROM articles WHERE id=?`, id).Scan(&tokenCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.ErrNotFound
		}
		return fmt.Errorf("store: SaveEnrichment token count: %w", err)
	}
	coverage := sentenceCoverage(e, tokenCount)

	const upsertEnrich = `INSERT INTO enrichments (article_id, enrichment)
                          VALUES (?,?)
                          ON CONFLICT(article_id) DO UPDATE SET enrichment=excluded.enrichment`
	if _, err := tx.ExecContext(ctx, upsertEnrich, id, string(eJSON)); err != nil {
		// A FOREIGN KEY violation means the parent article no longer exists —
		// it was deleted after the worker fetched it. Surface as ErrNotFound so
		// the caller can treat it as a benign race rather than a DB failure.
		if isSQLiteForeignKey(err) {
			return ports.ErrNotFound
		}
		return fmt.Errorf("store: SaveEnrichment upsert enrichment: %w", err)
	}

	const updArticle = `UPDATE articles SET status='enriched', enriched_at=?, updated_at=?, enrichment_coverage=?, raw_llm_response='' WHERE id=?`
	res, err := tx.ExecContext(ctx, updArticle, fmtTime(enrichedAt), fmtTime(now()), coverage, id)
	if err != nil {
		return fmt.Errorf("store: SaveEnrichment update article: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ports.ErrNotFound
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: SaveEnrichment commit: %w", err)
	}
	slog.Debug("store: enrichment saved", "article_id", id, "enrichment_bytes", len(eJSON))
	return nil
}

// SaveEnrichmentProgress persists a partial enrichment blob from an intermediate
// step-wise enrichment step and recomputes coverage, but leaves status unchanged
// so an interrupted article is still re-selected by ListWork and resumed. It does
// not stamp enriched_at (the article is not yet complete). The operation is
// atomic. Returns [ports.ErrNotFound] if the article was deleted.
func (s *SQLite) SaveEnrichmentProgress(ctx context.Context, id string, e model.Enrichment) error {
	eJSON, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("store: SaveEnrichmentProgress marshal: %w", err)
	}

	s.wmu.Lock()
	defer s.wmu.Unlock()

	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: SaveEnrichmentProgress begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var tokenCount int
	if err := tx.QueryRowContext(ctx, `SELECT json_array_length(tokens) FROM articles WHERE id=?`, id).Scan(&tokenCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.ErrNotFound
		}
		return fmt.Errorf("store: SaveEnrichmentProgress token count: %w", err)
	}
	coverage := sentenceCoverage(e, tokenCount)

	const upsertEnrich = `INSERT INTO enrichments (article_id, enrichment)
                          VALUES (?,?)
                          ON CONFLICT(article_id) DO UPDATE SET enrichment=excluded.enrichment`
	if _, err := tx.ExecContext(ctx, upsertEnrich, id, string(eJSON)); err != nil {
		if isSQLiteForeignKey(err) {
			return ports.ErrNotFound
		}
		return fmt.Errorf("store: SaveEnrichmentProgress upsert enrichment: %w", err)
	}

	const updArticle = `UPDATE articles SET updated_at=?, enrichment_coverage=? WHERE id=?`
	res, err := tx.ExecContext(ctx, updArticle, fmtTime(now()), coverage, id)
	if err != nil {
		return fmt.Errorf("store: SaveEnrichmentProgress update article: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ports.ErrNotFound
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: SaveEnrichmentProgress commit: %w", err)
	}
	slog.Debug("store: enrichment progress saved", "article_id", id, "coverage", coverage)
	return nil
}

// ListWork returns up to limit articles awaiting pipeline work — those in any
// of model.WorkStatuses (queued/fetching need fetch, fetched/enriching need
// enrich) — oldest first. The in-flight states are included so an article
// stuck by a crash mid-stage is re-selected and re-processed.
func (s *SQLite) ListWork(ctx context.Context, limit int) ([]model.Article, error) {
	const q = `SELECT id, source_url, url_hash, title, author, source_domain, lang,
                      original_text, tokens, summary, status, enrichment_version, error,
                      created_at, enriched_at, updated_at, pinned
               FROM articles
               WHERE status IN ('queued','fetching','fetched','enriching','topup_queued')
               ORDER BY created_at ASC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("store: ListWork: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var arts []model.Article
	for rows.Next() {
		a, err := scanArticleRow(rows)
		if err != nil {
			return nil, err
		}
		arts = append(arts, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: ListWork rows: %w", err)
	}
	return arts, nil
}

// SaveContent persists the fetched/extracted content for an article and
// advances its status to fetched (clearing any prior error). The token slice is
// JSON-encoded. Returns [ports.ErrNotFound] if the article was deleted while the
// fetch was in flight.
func (s *SQLite) SaveContent(ctx context.Context, id string, c ports.ContentUpdate) error {
	tokJSON, err := json.Marshal(c.Tokens)
	if err != nil {
		return fmt.Errorf("store: SaveContent marshal tokens: %w", err)
	}

	s.wmu.Lock()
	defer s.wmu.Unlock()

	const q = `UPDATE articles
	           SET source_url=?, title=?, author=?, source_domain=?, lang=?,
	               original_text=?, tokens=?, status='fetched', error='',
	               raw_llm_response='', updated_at=?
	           WHERE id=?`
	res, err := s.write.ExecContext(ctx, q,
		c.SourceURL, c.Title, c.Author, c.SourceDomain, c.Lang,
		c.Text, string(tokJSON), fmtTime(now()), id,
	)
	if err != nil {
		return fmt.Errorf("store: SaveContent: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ports.ErrNotFound
	}
	slog.Debug("store: article content saved", "article_id", id, "token_count", len(c.Tokens))
	return nil
}

// SaveSummary persists the article's summary text (the first step of the
// step-wise enrichment), stamping updated_at without touching status. Returns
// [ports.ErrNotFound] if the article was deleted in the meantime.
func (s *SQLite) SaveSummary(ctx context.Context, id, summary string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	const q = `UPDATE articles SET summary=?, updated_at=? WHERE id=?`
	res, err := s.write.ExecContext(ctx, q, summary, fmtTime(now()), id)
	if err != nil {
		return fmt.Errorf("store: SaveSummary: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ports.ErrNotFound
	}
	slog.Debug("store: article summary saved", "article_id", id, "summary_bytes", len(summary))
	return nil
}

// RetryArticle resets a failed article to the queue state for the stage that
// failed, clearing the error and bumping updated_at: a fetch_failed article
// goes back to queued (re-fetch), an enrich_failed article goes back to fetched
// (re-enrich only — its content is preserved). For any other (non-terminal-fail)
// status it is a no-op reset to a sensible queue state so a manual retry is
// always safe. Returns [ports.ErrNotFound] if the article does not exist.
func (s *SQLite) RetryArticle(ctx context.Context, id string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	// Route to the right queue state based on the current status. enrich_failed
	// (and the in-flight enriching) keep the fetched content; everything else
	// restarts from fetch.
	const q = `UPDATE articles
	           SET status = CASE
	                   WHEN status IN ('enrich_failed','enriching','fetched','enriched') THEN 'fetched'
	                   ELSE 'queued'
	               END,
	               error = '',
	               raw_llm_response = '',
	               enriched_at = '',
	               updated_at = ?
	           WHERE id = ?`
	res, err := s.write.ExecContext(ctx, q, fmtTime(now()), id)
	if err != nil {
		return fmt.Errorf("store: RetryArticle: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ports.ErrNotFound
	}
	slog.Debug("store: article queued for retry", "article_id", id)
	return nil
}

// ReEnrich queues an already-enriched article for re-enrichment. For
// model.ReEnrichModeTopup it sets status=topup_queued and KEEPS the existing
// enrichment blob (the worker merges in only the uncovered spans). For any other
// mode (the full re-translate) it resets status=fetched and clears enriched_at —
// the fetched content is preserved but the enrichment is regenerated and
// replaced wholesale by SaveEnrichment. In both cases the error is cleared and
// updated_at bumped so the change rides the next delta sync. Returns
// [ports.ErrNotFound] if the article does not exist.
func (s *SQLite) ReEnrich(ctx context.Context, id, mode string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	if mode == model.ReEnrichModeTopup {
		const q = `UPDATE articles SET status='topup_queued', error='', raw_llm_response='', updated_at=? WHERE id=?`
		res, err := s.write.ExecContext(ctx, q, fmtTime(now()), id)
		if err != nil {
			return fmt.Errorf("store: ReEnrich: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ports.ErrNotFound
		}
		slog.Debug("store: article queued for re-enrich", "article_id", id, "mode", mode)
		return nil
	}

	// Full re-enrich: clear the old enrichment blob and reset coverage to zero
	// so the worker starts fresh instead of resuming the previous partial run.
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: ReEnrich begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	const updArticle = `UPDATE articles
	                    SET status='fetched', error='', raw_llm_response='',
	                        enriched_at='', enrichment_coverage=0, updated_at=?
	                    WHERE id=?`
	res, err := tx.ExecContext(ctx, updArticle, fmtTime(now()), id)
	if err != nil {
		return fmt.Errorf("store: ReEnrich update article: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ports.ErrNotFound
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM enrichments WHERE article_id=?`, id); err != nil {
		return fmt.Errorf("store: ReEnrich delete enrichment: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: ReEnrich commit: %w", err)
	}
	slog.Debug("store: article queued for re-enrich", "article_id", id, "mode", mode)
	return nil
}

// SetPinned sets the article's pinned flag and bumps updated_at so the change
// rides the next delta sync. Returns [ports.ErrNotFound] if the article does not
// exist.
func (s *SQLite) SetPinned(ctx context.Context, id string, pinned bool) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	pinnedInt := 0
	if pinned {
		pinnedInt = 1
	}
	const q = `UPDATE articles SET pinned=?, updated_at=? WHERE id=?`
	res, err := s.write.ExecContext(ctx, q, pinnedInt, fmtTime(now()), id)
	if err != nil {
		return fmt.Errorf("store: SetPinned: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ports.ErrNotFound
	}
	slog.Debug("store: article pin updated", "article_id", id, "pinned", pinned)
	return nil
}

// ── Markdown budget ─────────────────────────────────────────────────────────

// markdownDay returns the current UTC day key ("YYYY-MM-DD") used to partition
// the markdown.new request-unit budget. The budget resets implicitly when the
// day key changes, since each day has its own counter row.
func markdownDay() string {
	return time.Now().UTC().Format("2006-01-02")
}

// MarkdownUnitsUsedToday returns the request units consumed during the current
// UTC day, or 0 when today's row does not exist yet.
func (s *SQLite) MarkdownUnitsUsedToday(ctx context.Context) (int, error) {
	var used int
	err := s.db.QueryRowContext(ctx,
		`SELECT units_used FROM markdown_usage WHERE day = ?`, markdownDay(),
	).Scan(&used)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: MarkdownUnitsUsedToday: %w", err)
	}
	return used, nil
}

// TryConsumeMarkdownUnits atomically reserves cost units against today's budget.
// The check-and-increment runs under the single write lock, so concurrent
// ingestions cannot both pass the limit. A dailyLimit <= 0 is treated as
// unlimited.
func (s *SQLite) TryConsumeMarkdownUnits(ctx context.Context, cost, dailyLimit int) (bool, int, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	day := markdownDay()

	// Ensure today's row exists so the subsequent UPDATE always matches.
	if _, err := s.write.ExecContext(ctx,
		`INSERT INTO markdown_usage (day, units_used) VALUES (?, 0)
		 ON CONFLICT(day) DO NOTHING`, day,
	); err != nil {
		return false, 0, fmt.Errorf("store: TryConsumeMarkdownUnits ensure row: %w", err)
	}

	var used int
	if err := s.write.QueryRowContext(ctx,
		`SELECT units_used FROM markdown_usage WHERE day = ?`, day,
	).Scan(&used); err != nil {
		return false, 0, fmt.Errorf("store: TryConsumeMarkdownUnits read: %w", err)
	}

	if dailyLimit > 0 && used+cost > dailyLimit {
		// Budget exhausted for today — reservation rejected, counter unchanged.
		return false, used, nil
	}

	if _, err := s.write.ExecContext(ctx,
		`UPDATE markdown_usage SET units_used = units_used + ? WHERE day = ?`, cost, day,
	); err != nil {
		return false, used, fmt.Errorf("store: TryConsumeMarkdownUnits update: %w", err)
	}
	usedAfter := used + cost
	slog.Debug("store: markdown units consumed", "day", day, "cost", cost, "used_after", usedAfter, "daily_limit", dailyLimit)
	return true, usedAfter, nil
}

// RefundMarkdownUnits returns cost units to today's budget, clamped at zero.
func (s *SQLite) RefundMarkdownUnits(ctx context.Context, cost int) error {
	if cost <= 0 {
		return nil
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()

	if _, err := s.write.ExecContext(ctx,
		`UPDATE markdown_usage SET units_used = MAX(0, units_used - ?) WHERE day = ?`,
		cost, markdownDay(),
	); err != nil {
		return fmt.Errorf("store: RefundMarkdownUnits: %w", err)
	}
	slog.Debug("store: markdown units refunded", "cost", cost)
	return nil
}

// ── Progress ──────────────────────────────────────────────────────────────────

// UpsertProgress stores progress with Last-Write-Wins semantics on UpdatedAt.
// Returns applied=true when the incoming record won (was stored), false when
// an existing record had a newer-or-equal UpdatedAt.
func (s *SQLite) UpsertProgress(ctx context.Context, p model.Progress) (bool, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	// Read current updated_at to decide LWW.
	var existingUpdatedAt string
	err := s.write.QueryRowContext(ctx,
		`SELECT updated_at FROM progress WHERE article_id=?`, p.ArticleID,
	).Scan(&existingUpdatedAt)

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("store: UpsertProgress read: %w", err)
	}

	if err == nil {
		// Row exists — apply LWW.
		existing, parseErr := parseTime(existingUpdatedAt)
		if parseErr != nil {
			return false, parseErr
		}
		if !existing.IsZero() && !p.UpdatedAt.After(existing) {
			// Incoming is older or equal — reject.
			slog.Debug("store: progress upsert rejected by LWW",
				"article_id", p.ArticleID,
				"incoming_updated_at", p.UpdatedAt,
				"existing_updated_at", existing,
			)
			return false, nil
		}
	}

	// Upsert.
	isRead := 0
	if p.IsRead {
		isRead = 1
	}
	const q = `INSERT INTO progress (article_id, position, is_read, updated_at)
               VALUES (?,?,?,?)
               ON CONFLICT(article_id) DO UPDATE SET
                   position=excluded.position,
                   is_read=excluded.is_read,
                   updated_at=excluded.updated_at`
	if _, err := s.write.ExecContext(ctx, q,
		p.ArticleID, p.Position, isRead, fmtTime(p.UpdatedAt),
	); err != nil {
		return false, fmt.Errorf("store: UpsertProgress write: %w", err)
	}
	return true, nil
}

// ListProgress returns progress records updated after since. Pass zero time
// for everything.
func (s *SQLite) ListProgress(ctx context.Context, since time.Time) ([]model.Progress, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if since.IsZero() {
		rows, err = s.db.QueryContext(ctx,
			`SELECT article_id, position, is_read, updated_at FROM progress`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT article_id, position, is_read, updated_at FROM progress WHERE updated_at > ?`,
			fmtTime(since))
	}
	if err != nil {
		return nil, fmt.Errorf("store: ListProgress: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var progs []model.Progress
	for rows.Next() {
		var pg model.Progress
		var isRead int
		var updatedAtStr string
		if err := rows.Scan(&pg.ArticleID, &pg.Position, &isRead, &updatedAtStr); err != nil {
			return nil, fmt.Errorf("store: ListProgress scan: %w", err)
		}
		pg.IsRead = isRead == 1
		if pg.UpdatedAt, err = parseTime(updatedAtStr); err != nil {
			return nil, err
		}
		progs = append(progs, pg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: ListProgress rows: %w", err)
	}
	if progs == nil {
		progs = []model.Progress{}
	}
	return progs, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// scanArticle scans a *sql.Row (single-row query) into *model.Article.
func scanArticle(row *sql.Row) (*model.Article, error) {
	var a model.Article
	var tokJSON, createdAtStr, enrichedAtStr, updatedAtStr string
	var pinned int
	if err := row.Scan(
		&a.ID, &a.SourceURL, &a.URLHash, &a.Title, &a.Author, &a.SourceDomain, &a.Lang,
		&a.OriginalText, &tokJSON, &a.Status, &a.EnrichmentVersion, &a.Error,
		&createdAtStr, &enrichedAtStr, &updatedAtStr, &pinned,
	); err != nil {
		return nil, err // let caller handle sql.ErrNoRows
	}
	a.Pinned = pinned == 1
	return finishArticle(&a, tokJSON, createdAtStr, enrichedAtStr, updatedAtStr)
}

// scanArticleRow scans a *sql.Rows (multi-row query) into *model.Article.
func scanArticleRow(rows *sql.Rows) (*model.Article, error) {
	var a model.Article
	var tokJSON, createdAtStr, enrichedAtStr, updatedAtStr string
	var pinned int
	if err := rows.Scan(
		&a.ID, &a.SourceURL, &a.URLHash, &a.Title, &a.Author, &a.SourceDomain, &a.Lang,
		&a.OriginalText, &tokJSON, &a.Summary, &a.Status, &a.EnrichmentVersion, &a.Error,
		&createdAtStr, &enrichedAtStr, &updatedAtStr, &pinned,
	); err != nil {
		return nil, fmt.Errorf("store: scanArticleRow: %w", err)
	}
	a.Pinned = pinned == 1
	return finishArticle(&a, tokJSON, createdAtStr, enrichedAtStr, updatedAtStr)
}

// finishArticle parses JSON and timestamps into the Article struct.
func finishArticle(a *model.Article, tokJSON, createdAtStr, enrichedAtStr, updatedAtStr string) (*model.Article, error) {
	if err := json.Unmarshal([]byte(tokJSON), &a.Tokens); err != nil {
		return nil, fmt.Errorf("store: unmarshal tokens for article %s: %w", a.ID, err)
	}
	if a.Tokens == nil {
		a.Tokens = []model.Token{}
	}
	var err error
	if a.CreatedAt, err = parseTime(createdAtStr); err != nil {
		return nil, err
	}
	if a.EnrichedAt, err = parseTime(enrichedAtStr); err != nil {
		return nil, err
	}
	if a.UpdatedAt, err = parseTime(updatedAtStr); err != nil {
		return nil, err
	}
	return a, nil
}

// isSQLiteUnique returns true when err represents a UNIQUE constraint violation
// from modernc.org/sqlite.
func isSQLiteUnique(err error) bool {
	if err == nil {
		return false
	}
	// modernc.org/sqlite wraps errors; the message contains "UNIQUE constraint
	// failed" (extended code 2067 = SQLITE_CONSTRAINT_UNIQUE).
	return strings.Contains(err.Error(), "UNIQUE")
}

// isSQLiteForeignKey returns true when err represents a FOREIGN KEY constraint
// violation from modernc.org/sqlite (extended code 787 =
// SQLITE_CONSTRAINT_FOREIGNKEY). It matches on the wrapped error message, which
// contains "FOREIGN KEY constraint failed".
func isSQLiteForeignKey(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "FOREIGN KEY")
}

// NewID is exported so sibling packages (e.g. ingest) can generate IDs with
// the same ULID scheme without importing a separate package.
func NewID() string {
	return newID()
}
