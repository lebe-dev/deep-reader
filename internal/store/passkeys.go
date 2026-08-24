package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"deep-reader/internal/ports"
)

// ── Auth: passkeys (WebAuthn credentials) ───────────────────────────────────

// CreatePasskey stores a newly registered credential. A UNIQUE violation on
// credential_id means the same authenticator is already registered, which is a
// dedup signal rather than a failure — the same mapping CreateArticle uses.
func (s *SQLite) CreatePasskey(ctx context.Context, p ports.Passkey) (ports.Passkey, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	p.ID = newID()
	p.CreatedAt = now()
	p.LastUsedAt = nil

	const q = `INSERT INTO webauthn_credentials (id, credential_id, name, credential, created_at)
	           VALUES (?, ?, ?, ?, ?)`
	if _, err := s.write.ExecContext(ctx, q, p.ID, p.CredentialID, p.Name, string(p.Credential), fmtTime(p.CreatedAt)); err != nil {
		if isSQLiteUnique(err) {
			return ports.Passkey{}, ports.ErrDuplicate
		}
		return ports.Passkey{}, fmt.Errorf("store: CreatePasskey: %w", err)
	}
	return p, nil
}

// ListPasskeys returns every registered passkey, newest first. The ULID id
// breaks ties within the same second, since created_at has second resolution.
func (s *SQLite) ListPasskeys(ctx context.Context) ([]ports.Passkey, error) {
	const q = `SELECT id, credential_id, name, credential, created_at, last_used_at
	           FROM webauthn_credentials
	           ORDER BY created_at DESC, id DESC`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: ListPasskeys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// Never nil: the HTTP layer would serialize a nil slice as JSON null and the
	// typed client throws during render.
	out := make([]ports.Passkey, 0, 4)
	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: ListPasskeys: %w", err)
	}
	return out, nil
}

// GetPasskeyByCredentialID resolves the credential a discoverable login
// asserted, or [ports.ErrNotFound].
func (s *SQLite) GetPasskeyByCredentialID(ctx context.Context, credentialID []byte) (ports.Passkey, error) {
	const q = `SELECT id, credential_id, name, credential, created_at, last_used_at
	           FROM webauthn_credentials WHERE credential_id = ?`
	p, err := scanPasskey(s.db.QueryRowContext(ctx, q, credentialID))
	if err != nil {
		return ports.Passkey{}, err
	}
	return p, nil
}

// TouchPasskey persists the credential state the WebAuthn library updated while
// validating an assertion (signature counter, backup flags) and stamps
// last_used_at. Without it the clone-detection counter would stay frozen at its
// registration value and the "last used" column would never move.
func (s *SQLite) TouchPasskey(ctx context.Context, id string, credential []byte, usedAt time.Time) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	const q = `UPDATE webauthn_credentials SET credential = ?, last_used_at = ? WHERE id = ?`
	res, err := s.write.ExecContext(ctx, q, string(credential), fmtTime(usedAt), id)
	if err != nil {
		return fmt.Errorf("store: TouchPasskey: %w", err)
	}
	return requireAffected(res, "TouchPasskey")
}

// RenamePasskey sets the display label of a passkey.
func (s *SQLite) RenamePasskey(ctx context.Context, id, name string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	res, err := s.write.ExecContext(ctx, `UPDATE webauthn_credentials SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return fmt.Errorf("store: RenamePasskey: %w", err)
	}
	return requireAffected(res, "RenamePasskey")
}

// DeletePasskey removes a passkey. The account always keeps its password, so
// deleting the last passkey can never lock the user out.
func (s *SQLite) DeletePasskey(ctx context.Context, id string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	res, err := s.write.ExecContext(ctx, `DELETE FROM webauthn_credentials WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: DeletePasskey: %w", err)
	}
	return requireAffected(res, "DeletePasskey")
}

// scanPasskey reads one webauthn_credentials row, mapping a missing row to
// [ports.ErrNotFound].
func scanPasskey(sc rowScanner) (ports.Passkey, error) {
	var (
		p            ports.Passkey
		credential   string
		createdAtStr string
		lastUsedStr  sql.NullString
	)
	if err := sc.Scan(&p.ID, &p.CredentialID, &p.Name, &credential, &createdAtStr, &lastUsedStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.Passkey{}, ports.ErrNotFound
		}
		return ports.Passkey{}, fmt.Errorf("store: scan passkey: %w", err)
	}
	p.Credential = []byte(credential)

	var err error
	if p.CreatedAt, err = parseTime(createdAtStr); err != nil {
		return ports.Passkey{}, err
	}
	if lastUsedStr.Valid {
		lastUsed, err := parseTime(lastUsedStr.String)
		if err != nil {
			return ports.Passkey{}, err
		}
		p.LastUsedAt = &lastUsed
	}
	return p, nil
}

// requireAffected maps an UPDATE/DELETE that matched no row to
// [ports.ErrNotFound], so callers can answer 404 without a preceding SELECT.
func requireAffected(res sql.Result, op string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: %s: rows affected: %w", op, err)
	}
	if n == 0 {
		return ports.ErrNotFound
	}
	return nil
}
