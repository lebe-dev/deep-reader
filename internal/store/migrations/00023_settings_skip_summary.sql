-- +goose Up

-- skip_summary turns off the summary step of the step-wise enrichment: with it
-- enabled the worker goes straight to translating the chunks, saving one LLM
-- call per article (at the cost of the cross-chunk context the summary provides
-- and of the library card's summary).
--
-- Default 0 (off) — summaries keep being generated exactly as before.
ALTER TABLE settings ADD COLUMN skip_summary INTEGER NOT NULL DEFAULT 0;

-- +goose Down

ALTER TABLE settings DROP COLUMN skip_summary;
