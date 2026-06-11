package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"deep-reader/internal/config"
	"deep-reader/internal/model"
	"deep-reader/internal/ports"
)

const llmProviderColumns = "id, name, base_url, api_key, model, is_active, force_json_object, created_at, updated_at"

// ListLLMProviders returns all configured profiles ordered by creation time.
func (s *SQLite) ListLLMProviders(ctx context.Context) ([]model.LLMProvider, error) {
	const q = `SELECT ` + llmProviderColumns + ` FROM llm_providers ORDER BY created_at, id`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: ListLLMProviders: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var providers []model.LLMProvider
	for rows.Next() {
		p, err := scanLLMProvider(rows)
		if err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	return providers, rows.Err()
}

// GetActiveLLMProvider returns the sole active profile, or ports.ErrNotFound when
// none is configured.
func (s *SQLite) GetActiveLLMProvider(ctx context.Context) (model.LLMProvider, error) {
	const q = `SELECT ` + llmProviderColumns + ` FROM llm_providers WHERE is_active = 1 LIMIT 1`
	p, err := scanLLMProvider(s.db.QueryRowContext(ctx, q))
	if errors.Is(err, sql.ErrNoRows) {
		return model.LLMProvider{}, ports.ErrNotFound
	}
	if err != nil {
		return model.LLMProvider{}, fmt.Errorf("store: GetActiveLLMProvider: %w", err)
	}
	return p, nil
}

// CreateLLMProvider inserts a new profile. The first profile in the table becomes
// active so the deployment always has an active connection.
func (s *SQLite) CreateLLMProvider(ctx context.Context, p model.LLMProvider) (model.LLMProvider, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	if p.ID == "" {
		p.ID = newID()
	}
	t := now()
	p.CreatedAt = t
	p.UpdatedAt = t

	var count int
	if err := s.write.QueryRowContext(ctx, `SELECT COUNT(*) FROM llm_providers`).Scan(&count); err != nil {
		return model.LLMProvider{}, fmt.Errorf("store: CreateLLMProvider count: %w", err)
	}
	p.IsActive = count == 0

	const q = `INSERT INTO llm_providers (` + llmProviderColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := s.write.ExecContext(ctx, q,
		p.ID, p.Name, p.BaseURL, p.APIKey, p.Model, boolToInt(p.IsActive), boolToInt(p.ForceJSONObject), fmtTime(p.CreatedAt), fmtTime(p.UpdatedAt),
	); err != nil {
		return model.LLMProvider{}, fmt.Errorf("store: CreateLLMProvider insert: %w", err)
	}
	slog.Debug("store: llm provider created", "id", p.ID, "name", p.Name, "active", p.IsActive)
	return p, nil
}

// UpdateLLMProvider applies the input to the profile id. A nil input.APIKey leaves
// the stored key unchanged.
func (s *SQLite) UpdateLLMProvider(ctx context.Context, id string, in model.LLMProviderInput) (model.LLMProvider, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	const selQ = `SELECT ` + llmProviderColumns + ` FROM llm_providers WHERE id = ?`
	cur, err := scanLLMProvider(s.write.QueryRowContext(ctx, selQ, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.LLMProvider{}, ports.ErrNotFound
	}
	if err != nil {
		return model.LLMProvider{}, fmt.Errorf("store: UpdateLLMProvider read: %w", err)
	}

	cur.Name = in.Name
	cur.BaseURL = in.BaseURL
	cur.Model = in.Model
	cur.ForceJSONObject = in.ForceJSONObject
	if in.APIKey != nil {
		cur.APIKey = *in.APIKey
	}
	cur.UpdatedAt = now()

	const updQ = `UPDATE llm_providers SET name = ?, base_url = ?, api_key = ?, model = ?, force_json_object = ?, updated_at = ? WHERE id = ?`
	if _, err := s.write.ExecContext(ctx, updQ,
		cur.Name, cur.BaseURL, cur.APIKey, cur.Model, boolToInt(cur.ForceJSONObject), fmtTime(cur.UpdatedAt), id,
	); err != nil {
		return model.LLMProvider{}, fmt.Errorf("store: UpdateLLMProvider write: %w", err)
	}
	slog.Debug("store: llm provider updated", "id", id, "name", cur.Name)
	return cur, nil
}

// DeleteLLMProvider removes the profile id, promoting the most-recently-created
// remaining profile to active if the deleted one was active. The delete and the
// conditional promote run in a single transaction so a crash between them can
// never leave the deployment with zero active providers: either both land or
// neither does.
func (s *SQLite) DeleteLLMProvider(ctx context.Context, id string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	var wasActive int
	err := s.write.QueryRowContext(ctx, `SELECT is_active FROM llm_providers WHERE id = ?`, id).Scan(&wasActive)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("store: DeleteLLMProvider read: %w", err)
	}

	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: DeleteLLMProvider begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM llm_providers WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: DeleteLLMProvider delete: %w", err)
	}

	if wasActive != 0 {
		// Promote the newest remaining profile so an active connection always
		// exists while any profile remains. The subquery yields no row when the
		// table is now empty, so the UPDATE is a safe no-op in that case.
		const promoteQ = `UPDATE llm_providers SET is_active = 1
		                  WHERE id = (SELECT id FROM llm_providers ORDER BY created_at DESC, id DESC LIMIT 1)`
		if _, err := tx.ExecContext(ctx, promoteQ); err != nil {
			return fmt.Errorf("store: DeleteLLMProvider promote: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: DeleteLLMProvider commit: %w", err)
	}
	return nil
}

// SetActiveLLMProvider makes profile id the sole active profile. The clear-then-set
// pair runs in one transaction so the partial unique index never sees two active
// rows.
func (s *SQLite) SetActiveLLMProvider(ctx context.Context, id string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()

	var exists int
	err := s.write.QueryRowContext(ctx, `SELECT 1 FROM llm_providers WHERE id = ?`, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("store: SetActiveLLMProvider read: %w", err)
	}

	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: SetActiveLLMProvider begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `UPDATE llm_providers SET is_active = 0 WHERE is_active = 1`); err != nil {
		return fmt.Errorf("store: SetActiveLLMProvider clear: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE llm_providers SET is_active = 1, updated_at = ? WHERE id = ?`, fmtTime(now()), id); err != nil {
		return fmt.Errorf("store: SetActiveLLMProvider set: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: SetActiveLLMProvider commit: %w", err)
	}
	slog.Debug("store: llm provider activated", "id", id)
	return nil
}

// seedLLMProvider inserts a single "Default" profile from the LLM_* env vars on
// first boot when the table is empty, preserving env-configured deployments. Once
// any profile exists the UI is the source of truth and this is a no-op.
func (s *SQLite) seedLLMProvider(ctx context.Context, cfg *config.Config) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM llm_providers`).Scan(&count); err != nil {
		return fmt.Errorf("store: seedLLMProvider count: %w", err)
	}
	if count > 0 {
		return nil
	}
	// No LLM env config at all — nothing to seed, and the user will configure a
	// provider through the UI. This is the expected fresh-install path.
	if cfg.LLMAPIKey == "" && cfg.LLMAPIBaseURL == "" && cfg.LLMModel == "" {
		return nil
	}
	// Some LLM env config is present but incomplete: a seeded profile missing
	// base_url or model would later fail LLMProviderInput.Validate() (and every
	// LLM call), so do not persist a broken profile. Warn loudly instead so the
	// operator knows their partial env config was ignored and must finish the
	// profile in the UI.
	if cfg.LLMAPIBaseURL == "" || cfg.LLMModel == "" {
		slog.Warn("store: skipping LLM provider seed — incomplete env config (LLM_API_BASE_URL and LLM_MODEL are both required); configure a provider in Settings > LLM",
			"has_base_url", cfg.LLMAPIBaseURL != "",
			"has_model", cfg.LLMModel != "",
			"has_api_key", cfg.LLMAPIKey != "",
		)
		return nil
	}
	if _, err := s.CreateLLMProvider(ctx, model.LLMProvider{
		Name:    "Default",
		BaseURL: cfg.LLMAPIBaseURL,
		APIKey:  cfg.LLMAPIKey,
		Model:   cfg.LLMModel,
	}); err != nil {
		return fmt.Errorf("store: seedLLMProvider: %w", err)
	}
	slog.Info("store: seeded default LLM provider from env")
	return nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows so scanLLMProvider works
// for single-row and multi-row reads alike.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanLLMProvider(row rowScanner) (model.LLMProvider, error) {
	var p model.LLMProvider
	var active, forceJSONObject int
	var createdAt, updatedAt string
	if err := row.Scan(&p.ID, &p.Name, &p.BaseURL, &p.APIKey, &p.Model, &active, &forceJSONObject, &createdAt, &updatedAt); err != nil {
		return model.LLMProvider{}, err
	}
	p.IsActive = active != 0
	p.ForceJSONObject = forceJSONObject != 0
	var err error
	if p.CreatedAt, err = parseTime(createdAt); err != nil {
		return model.LLMProvider{}, err
	}
	if p.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return model.LLMProvider{}, err
	}
	return p, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
