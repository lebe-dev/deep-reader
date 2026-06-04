-- +goose Up

-- chunk_tokens: the user-tunable step-wise enrichment window size (target tokens
-- per per-chunk LLM call), editable from the LLM settings tab. 0 means "use the
-- deployment default" (config.LLMChunkTokens / env LLM_CHUNK_TOKENS); a non-zero
-- value is bounded by [model.MinChunkTokens, model.MaxChunkTokens]. Stored on the
-- settings singleton so it syncs across the user's devices like other settings.
ALTER TABLE settings ADD COLUMN chunk_tokens INTEGER NOT NULL DEFAULT 0;

-- +goose Down

ALTER TABLE settings DROP COLUMN chunk_tokens;
