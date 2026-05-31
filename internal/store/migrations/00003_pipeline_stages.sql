-- +goose Up

-- Migrate the legacy 3-state status model to the explicit fetch→enrich pipeline.
--
-- Legacy semantics:
--   pending  — content was already fetched synchronously at ingest, waiting for
--              enrichment  → maps to the new 'fetched' (needs enrich) state.
--   failed   — only the enrichment stage could fail (fetch was synchronous and
--              never created a record) → maps to 'enrich_failed'.
--   enriched — terminal success, unchanged.
UPDATE articles SET status = 'fetched'       WHERE status = 'pending';
UPDATE articles SET status = 'enrich_failed' WHERE status = 'failed';

-- +goose Down

UPDATE articles SET status = 'pending' WHERE status IN ('queued', 'fetching', 'fetched', 'enriching');
UPDATE articles SET status = 'failed'  WHERE status IN ('fetch_failed', 'enrich_failed');
