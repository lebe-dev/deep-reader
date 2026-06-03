-- +goose Up

-- raw_llm_response: the verbatim model output captured when the enrichment
-- stage fails to decode the LLM response (e.g. truncated/invalid JSON). It is
-- only meaningful alongside status='enrich_failed' and lets the UI surface the
-- raw answer for inspection. Cleared whenever the article re-enters the
-- pipeline or enriches successfully.
ALTER TABLE articles ADD COLUMN raw_llm_response TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE articles DROP COLUMN raw_llm_response;
