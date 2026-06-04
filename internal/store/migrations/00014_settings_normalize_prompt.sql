-- +goose Up

-- normalize_prompt: the user's custom content-normalization system-prompt
-- template, editable from the LLM settings tab. The normalization step runs in
-- the fetch stage (after extraction, before tokenization) and strips leftover
-- navigation / chrome / boilerplate that the extractor leaked into the article
-- body. An empty string means "use the built-in default template"
-- (normalize.DefaultPromptTemplate). Stored on the settings singleton so it
-- syncs across the user's devices like other settings.
ALTER TABLE settings ADD COLUMN normalize_prompt TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE settings DROP COLUMN normalize_prompt;
