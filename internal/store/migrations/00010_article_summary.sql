-- +goose Up

-- summary: a short LLM-produced abstract of the article, generated as the first
-- step of the step-wise enrichment (before the per-chunk translation passes). It
-- is shown to the reader and also fed back as context into each chunk's prompt
-- so translations stay consistent across chunks. Empty until summarized.
ALTER TABLE articles ADD COLUMN summary TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE articles DROP COLUMN summary;
