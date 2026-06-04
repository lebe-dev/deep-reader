-- +goose Up

-- summary_prompt: the user's custom summary system-prompt template (the first
-- step of the step-wise enrichment), editable from the LLM settings tab. An
-- empty string means "use the built-in default template"
-- (llm.DefaultSummaryPromptTemplate). Stored on the settings singleton so it
-- syncs across the user's devices like other settings.
ALTER TABLE settings ADD COLUMN summary_prompt TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE settings DROP COLUMN summary_prompt;
