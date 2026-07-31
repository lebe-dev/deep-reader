package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"deep-reader/internal/model"
	"deep-reader/internal/ports"
)

// ── Publications ──────────────────────────────────────────────────────────────

// publicationCols is the column list every publication read shares.
const publicationCols = `token, article_id, title, description, published_at, expires_at`

// ReplacePublication publishes an article, returning the token of the
// publication it superseded (empty when the article was not published before)
// so the caller can delete that page's file.
//
// Re-publishing deliberately mints a NEW token rather than refreshing the old
// one's expiry: the previous link was handed out under the TTL in force at the
// time, and silently extending it would break the promise the user made when
// they shared it.
func (s *SQLite) ReplacePublication(ctx context.Context, p model.Publication) (string, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("store: ReplacePublication begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var previous string
	err = tx.QueryRowContext(ctx, `SELECT token FROM publications WHERE article_id = ?`, p.ArticleID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("store: ReplacePublication read previous: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM publications WHERE article_id = ?`, p.ArticleID); err != nil {
		return "", fmt.Errorf("store: ReplacePublication delete previous: %w", err)
	}

	const insQ = `INSERT INTO publications (` + publicationCols + `) VALUES (?, ?, ?, ?, ?, ?)`
	if _, err := tx.ExecContext(ctx, insQ,
		p.Token, p.ArticleID, p.Title, p.Description, fmtTime(p.PublishedAt), fmtTime(p.ExpiresAt),
	); err != nil {
		return "", fmt.Errorf("store: ReplacePublication insert: %w", err)
	}

	// Publishing changes what the library shows for this article (the globe and
	// its link), and the client only re-reads articles whose updated_at moved —
	// so the change has to ride the delta sync like any other metadata edit.
	if err := touchArticle(ctx, tx, p.ArticleID); err != nil {
		return "", err
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("store: ReplacePublication commit: %w", err)
	}
	return previous, nil
}

// GetPublication returns the publication for token, or ports.ErrNotFound. It
// does NOT filter expired rows: the caller decides how an expired link is
// answered, and the sweeper is what eventually removes it.
func (s *SQLite) GetPublication(ctx context.Context, token string) (model.Publication, error) {
	const q = `SELECT ` + publicationCols + ` FROM publications WHERE token = ?`
	return scanPublication(s.db.QueryRowContext(ctx, q, token))
}

// GetPublicationByArticle returns the article's publication, or
// ports.ErrNotFound when it is not published.
func (s *SQLite) GetPublicationByArticle(ctx context.Context, articleID string) (model.Publication, error) {
	const q = `SELECT ` + publicationCols + ` FROM publications WHERE article_id = ?`
	return scanPublication(s.db.QueryRowContext(ctx, q, articleID))
}

// DeletePublicationByArticle unpublishes an article and returns the token whose
// page file the caller must now delete. Returns ports.ErrNotFound when the
// article was not published.
func (s *SQLite) DeletePublicationByArticle(ctx context.Context, articleID string) (string, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	var token string
	err := s.write.QueryRowContext(ctx, `SELECT token FROM publications WHERE article_id = ?`, articleID).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ports.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: DeletePublicationByArticle read: %w", err)
	}

	if _, err := s.write.ExecContext(ctx, `DELETE FROM publications WHERE article_id = ?`, articleID); err != nil {
		return "", fmt.Errorf("store: DeletePublicationByArticle delete: %w", err)
	}
	// Revoking is a library-visible change too — see ReplacePublication.
	if err := touchArticle(ctx, s.write, articleID); err != nil {
		return "", err
	}
	return token, nil
}

// execer is the subset of *sql.DB / *sql.Tx that touchArticle needs, so it can
// run either inside a transaction or on the write connection directly.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// touchArticle bumps an article's updated_at so a change that lives outside the
// articles table (here: its publication) still rides the client's delta sync,
// which selects on updated_at alone.
func touchArticle(ctx context.Context, db execer, articleID string) error {
	if _, err := db.ExecContext(ctx, `UPDATE articles SET updated_at = ? WHERE id = ?`, fmtTime(now()), articleID); err != nil {
		return fmt.Errorf("store: touchArticle: %w", err)
	}
	return nil
}

// PruneExpiredPublications removes every publication whose TTL elapsed at or
// before cutoff and returns the tokens it removed, so the caller can delete the
// corresponding page files. Publications stored without an expiry are never
// pruned.
func (s *SQLite) PruneExpiredPublications(ctx context.Context, cutoff time.Time) ([]string, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	const selQ = `SELECT token FROM publications WHERE expires_at != '' AND expires_at <= ?`
	rows, err := s.write.QueryContext(ctx, selQ, fmtTime(cutoff))
	if err != nil {
		return nil, fmt.Errorf("store: PruneExpiredPublications select: %w", err)
	}
	tokens, err := scanTokens(rows)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, nil
	}

	const delQ = `DELETE FROM publications WHERE expires_at != '' AND expires_at <= ?`
	if _, err := s.write.ExecContext(ctx, delQ, fmtTime(cutoff)); err != nil {
		return nil, fmt.Errorf("store: PruneExpiredPublications delete: %w", err)
	}
	return tokens, nil
}

// ListPublicationTokens returns the tokens of every live publication. It backs
// the orphaned-file sweep, which is how a page file whose row vanished with its
// article (ON DELETE CASCADE) is eventually removed from disk.
func (s *SQLite) ListPublicationTokens(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT token FROM publications`)
	if err != nil {
		return nil, fmt.Errorf("store: ListPublicationTokens: %w", err)
	}
	return scanTokens(rows)
}

func scanTokens(rows *sql.Rows) ([]string, error) {
	defer func() { _ = rows.Close() }()

	var tokens []string
	for rows.Next() {
		var token string
		if err := rows.Scan(&token); err != nil {
			return nil, fmt.Errorf("store: scanTokens: %w", err)
		}
		tokens = append(tokens, token)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: scanTokens rows: %w", err)
	}
	return tokens, nil
}

func scanPublication(row *sql.Row) (model.Publication, error) {
	var p model.Publication
	var publishedAt, expiresAt string
	if err := row.Scan(&p.Token, &p.ArticleID, &p.Title, &p.Description, &publishedAt, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Publication{}, ports.ErrNotFound
		}
		return model.Publication{}, fmt.Errorf("store: scanPublication: %w", err)
	}

	var err error
	if p.PublishedAt, err = parseTime(publishedAt); err != nil {
		return model.Publication{}, err
	}
	if p.ExpiresAt, err = parseTime(expiresAt); err != nil {
		return model.Publication{}, err
	}
	return p, nil
}
