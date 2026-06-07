-- +goose Up

-- progress_stage is a short, human-readable label of the pipeline step an
-- article is currently in (e.g. "Fetching content", "Summarizing",
-- "Translating (3/5)"). The enrichment worker updates it as it advances through
-- the fetch → normalize → summarize → translate stages so the UI can show what
-- is happening during processing instead of a bare "Processing…". It is empty
-- for articles at rest (queued, enriched, or failed — the latter two convey
-- their outcome through status/error instead).
ALTER TABLE articles ADD COLUMN progress_stage TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE articles DROP COLUMN progress_stage;
