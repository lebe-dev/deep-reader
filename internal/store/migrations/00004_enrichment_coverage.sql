-- +goose Up

-- enrichment_coverage records the fraction [0,1] of an article's tokens that
-- fall within at least one sentence translation. It is the completeness signal
-- the UI surfaces so the reader can tell when the LLM stopped annotating partway
-- through (a low value means the tail of the article was left untranslated).
-- New enrichments set it accurately from Go (store.sentenceCoverage); this
-- migration backfills already-enriched rows with a best-effort approximation
-- based on the furthest-covered sentence index.
ALTER TABLE articles ADD COLUMN enrichment_coverage REAL NOT NULL DEFAULT 0;

-- +goose StatementBegin
UPDATE articles
SET enrichment_coverage = (
    SELECT COALESCE(
        MIN(1.0, CAST(MAX(json_extract(s.value, '$.end_index')) + 1 AS REAL)
                 / json_array_length(articles.tokens)),
        0)
    FROM enrichments e, json_each(json_extract(e.enrichment, '$.sentences')) s
    WHERE e.article_id = articles.id
)
WHERE status = 'enriched' AND json_array_length(tokens) > 0;
-- +goose StatementEnd

-- +goose Down

ALTER TABLE articles DROP COLUMN enrichment_coverage;
