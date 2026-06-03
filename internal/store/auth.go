package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"deep-reader/internal/model"
	"deep-reader/internal/ports"
)

// ── Auth: built-in user ─────────────────────────────────────────────────────

// IsInitialized reports whether the single built-in account exists.
func (s *SQLite) IsInitialized(ctx context.Context) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_user WHERE id = 1`).Scan(&n); err != nil {
		return false, fmt.Errorf("store: IsInitialized: %w", err)
	}
	return n > 0, nil
}

// CreateUser creates the single built-in account. It returns
// [ports.ErrAlreadyInitialized] if the account already exists, so setup stays a
// one-time operation even under a race (the CHECK(id=1) primary key makes the
// second insert fail with a UNIQUE violation).
func (s *SQLite) CreateUser(ctx context.Context, username, passwordHash string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	ts := fmtTime(now())
	const q = `INSERT INTO app_user (id, username, password_hash, created_at, updated_at)
	           VALUES (1, ?, ?, ?, ?)`
	if _, err := s.write.ExecContext(ctx, q, username, passwordHash, ts, ts); err != nil {
		if isSQLiteUnique(err) {
			return ports.ErrAlreadyInitialized
		}
		return fmt.Errorf("store: CreateUser: %w", err)
	}
	slog.Debug("store: built-in user created", "username", username)
	return nil
}

// GetUser returns the built-in account, or [ports.ErrNotFound] if the service is
// not yet initialized.
func (s *SQLite) GetUser(ctx context.Context) (*model.User, error) {
	const q = `SELECT username, password_hash, created_at, updated_at
	           FROM app_user WHERE id = 1`
	var u model.User
	var createdAtStr, updAtStr string
	if err := s.db.QueryRowContext(ctx, q).Scan(&u.Username, &u.PasswordHash, &createdAtStr, &updAtStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ports.ErrNotFound
		}
		return nil, fmt.Errorf("store: GetUser: %w", err)
	}
	var err error
	if u.CreatedAt, err = parseTime(createdAtStr); err != nil {
		return nil, err
	}
	if u.UpdatedAt, err = parseTime(updAtStr); err != nil {
		return nil, err
	}
	return &u, nil
}

// ── Auth: sessions ──────────────────────────────────────────────────────────

// CreateSession persists a login session keyed by the SHA-256 hash of its bearer
// token.
func (s *SQLite) CreateSession(ctx context.Context, tokenHash string, createdAt time.Time) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	const q = `INSERT INTO sessions (token_hash, created_at) VALUES (?, ?)
	           ON CONFLICT(token_hash) DO NOTHING`
	if _, err := s.write.ExecContext(ctx, q, tokenHash, fmtTime(createdAt)); err != nil {
		return fmt.Errorf("store: CreateSession: %w", err)
	}
	return nil
}

// SessionExists reports whether a session with the given token hash exists.
func (s *SQLite) SessionExists(ctx context.Context, tokenHash string) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE token_hash = ?`, tokenHash,
	).Scan(&n); err != nil {
		return false, fmt.Errorf("store: SessionExists: %w", err)
	}
	return n > 0, nil
}

// DeleteSession removes the session with the given token hash (logout). Removing
// a non-existent session is not an error.
func (s *SQLite) DeleteSession(ctx context.Context, tokenHash string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	if _, err := s.write.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("store: DeleteSession: %w", err)
	}
	return nil
}
