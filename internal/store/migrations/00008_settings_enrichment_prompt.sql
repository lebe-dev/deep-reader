-- +goose Up

-- enrichment_prompt: the user's custom enrichment system-prompt template,
-- editable from the LLM settings tab. An empty string means "use the built-in
-- default template" (llm.DefaultEnrichmentPromptTemplate). Stored on the
-- settings singleton so it syncs across the user's devices like other settings.
ALTER TABLE settings ADD COLUMN enrichment_prompt TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE settings DROP COLUMN enrichment_prompt;
