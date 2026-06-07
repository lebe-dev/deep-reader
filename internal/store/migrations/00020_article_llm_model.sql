-- +goose Up

-- llm_model: the model name (e.g. "gpt-4o-mini", "anthropic/claude-3.5-sonnet")
-- that produced the article's enrichment, recorded when the article is flipped
-- to status=enriched. It is the effective model of the active LLM profile at
-- processing time, so a later profile/model change does not rewrite history.
-- Empty until the article has been enriched (or for articles enriched before
-- this column existed).
ALTER TABLE articles ADD COLUMN llm_model TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE articles DROP COLUMN llm_model;
