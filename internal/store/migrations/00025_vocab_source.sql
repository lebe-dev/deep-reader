-- +goose Up

-- How the term entered the vocabulary: 'tap' (passive capture — the user tapped
-- a word the LLM had annotated) or 'manual' (saved deliberately from the
-- reader's action menu, with its translation fetched on demand). Existing rows
-- predate manual saving, so 'tap' is the correct default for all of them.
ALTER TABLE lookup_events ADD COLUMN source TEXT NOT NULL DEFAULT 'tap';

-- Mirrors the newest event's source on the aggregate, like the other latest_*
-- fields: it is recomputed from lookup_events, never written independently.
ALTER TABLE vocab_entries ADD COLUMN latest_source TEXT NOT NULL DEFAULT 'tap';

-- Custom system-prompt template for the on-demand translation of one saved word
-- or phrase. Empty means the built-in default (llm.DefaultTranslatePromptTemplate),
-- matching the other prompt columns.
ALTER TABLE settings ADD COLUMN translate_prompt TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE settings DROP COLUMN translate_prompt;
ALTER TABLE vocab_entries DROP COLUMN latest_source;
ALTER TABLE lookup_events DROP COLUMN source;
