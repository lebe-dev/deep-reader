-- +goose Up

-- markdown_usage: one row per UTC day tracking how many markdown.new request
-- units were consumed. The free plan grants a fixed daily budget that resets
-- daily; we model that as a per-day counter and only ever touch today's row.
-- Old rows are harmless and left in place as a lightweight audit trail.
CREATE TABLE IF NOT EXISTS markdown_usage (
    day        TEXT    PRIMARY KEY,           -- 'YYYY-MM-DD' in UTC
    units_used INTEGER NOT NULL DEFAULT 0
);

-- +goose Down

DROP TABLE IF EXISTS markdown_usage;
