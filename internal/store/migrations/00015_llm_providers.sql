-- +goose Up

-- llm_providers: user-managed LLM connection profiles, edited from Settings > LLM
-- (only when a backend is reachable). Each row is a full connection: a display
-- name, the OpenAI-compatible base URL, the secret API key (stored as-is but
-- never returned whole to the client — the API masks it), and the model name.
-- Exactly one row may be active (is_active = 1), enforced by the partial unique
-- index below; the active profile supplies the connection for every LLM call at
-- request time. The table is seeded once from the LLM_* env vars on first boot
-- when empty, after which the UI is the source of truth.
CREATE TABLE IF NOT EXISTS llm_providers (
    id         TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL,
    base_url   TEXT    NOT NULL,
    api_key    TEXT    NOT NULL DEFAULT '',
    model      TEXT    NOT NULL,
    is_active  INTEGER NOT NULL DEFAULT 0,
    created_at TEXT    NOT NULL,
    updated_at TEXT    NOT NULL
);

-- At most one active profile at any time.
CREATE UNIQUE INDEX IF NOT EXISTS idx_llm_providers_active
    ON llm_providers (is_active) WHERE is_active = 1;

-- +goose Down

DROP INDEX IF EXISTS idx_llm_providers_active;
DROP TABLE IF EXISTS llm_providers;
