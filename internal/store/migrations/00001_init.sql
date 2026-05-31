-- +goose Up

-- Settings: singleton row for user-tunable preferences.
-- We enforce exactly one row via CHECK(id = 1).
CREATE TABLE IF NOT EXISTS settings (
    id                          INTEGER PRIMARY KEY CHECK(id = 1),
    cefr_level                  TEXT    NOT NULL DEFAULT 'A2',
    target_language             TEXT    NOT NULL DEFAULT 'ru',
    llm_model                   TEXT    NOT NULL DEFAULT '',
    min_difficulty_to_highlight TEXT    NOT NULL DEFAULT 'B1',
    updated_at                  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Seed the singleton defaults on first migration.
INSERT OR IGNORE INTO settings (id, cefr_level, target_language, llm_model, min_difficulty_to_highlight)
VALUES (1, 'A2', 'ru', '', 'B1');

-- Articles: core record with tokenised text stored as JSON blob.
CREATE TABLE IF NOT EXISTS articles (
    id                 TEXT    PRIMARY KEY,
    source_url         TEXT    NOT NULL,
    url_hash           TEXT    NOT NULL,
    title              TEXT    NOT NULL DEFAULT '',
    author             TEXT    NOT NULL DEFAULT '',
    source_domain      TEXT    NOT NULL DEFAULT '',
    lang               TEXT    NOT NULL DEFAULT '',
    original_text      TEXT    NOT NULL DEFAULT '',
    tokens             TEXT    NOT NULL DEFAULT '[]',  -- JSON array of model.Token
    status             TEXT    NOT NULL DEFAULT 'pending',
    enrichment_version INTEGER NOT NULL DEFAULT 0,
    error              TEXT    NOT NULL DEFAULT '',
    created_at         TEXT    NOT NULL,
    enriched_at        TEXT    NOT NULL DEFAULT '',
    updated_at         TEXT    NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_articles_url_hash ON articles(url_hash);
CREATE INDEX IF NOT EXISTS idx_articles_updated_at ON articles(updated_at);
CREATE INDEX IF NOT EXISTS idx_articles_status ON articles(status);

-- Enrichments: one row per article, holds the full LLM-produced JSON blob.
-- Cascade delete keeps the FK child consistent when an article is removed.
CREATE TABLE IF NOT EXISTS enrichments (
    article_id   TEXT    PRIMARY KEY REFERENCES articles(id) ON DELETE CASCADE,
    enrichment   TEXT    NOT NULL DEFAULT '{}'  -- JSON blob of model.Enrichment
);

-- Progress: reading position per article, synced with LWW on updated_at.
CREATE TABLE IF NOT EXISTS progress (
    article_id TEXT    PRIMARY KEY REFERENCES articles(id) ON DELETE CASCADE,
    position   INTEGER NOT NULL DEFAULT 0,
    is_read    INTEGER NOT NULL DEFAULT 0,  -- SQLite boolean: 0/1
    updated_at TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_progress_updated_at ON progress(updated_at);

-- +goose Down

DROP INDEX IF EXISTS idx_progress_updated_at;
DROP TABLE IF EXISTS progress;
DROP TABLE IF EXISTS enrichments;
DROP INDEX IF EXISTS idx_articles_status;
DROP INDEX IF EXISTS idx_articles_updated_at;
DROP INDEX IF EXISTS idx_articles_url_hash;
DROP TABLE IF EXISTS articles;
DROP TABLE IF EXISTS settings;
