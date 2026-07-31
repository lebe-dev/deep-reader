-- +goose Up

-- Append-only log of translation lookups (the user tapped a word/phrase in the
-- reader). Deliberately NOT foreign-keyed to articles: deleting an article must
-- not erase the vocabulary built from it, which is why article_title is
-- denormalized here.
CREATE TABLE IF NOT EXISTS lookup_events (
    id            TEXT    PRIMARY KEY,
    entry_key     TEXT    NOT NULL,
    kind          TEXT    NOT NULL,
    article_id    TEXT    NOT NULL DEFAULT '',
    article_title TEXT    NOT NULL DEFAULT '',
    span_start    INTEGER NOT NULL,
    span_end      INTEGER NOT NULL,
    surface       TEXT    NOT NULL DEFAULT '',
    lemma         TEXT    NOT NULL DEFAULT '',
    translation   TEXT    NOT NULL DEFAULT '',
    cefr_level    TEXT    NOT NULL DEFAULT '',
    phrase_type   TEXT    NOT NULL DEFAULT '',
    context       TEXT    NOT NULL DEFAULT '',
    occurred_at   TEXT    NOT NULL,
    created_at    TEXT    NOT NULL
);

-- One occurrence per distinct position: re-tapping the same word in the same
-- place is not a new occurrence, on this device or any other.
CREATE UNIQUE INDEX IF NOT EXISTS idx_lookup_events_span
    ON lookup_events(article_id, kind, span_start);
CREATE INDEX IF NOT EXISTS idx_lookup_events_entry ON lookup_events(entry_key);

-- Aggregate per lemma — derived from lookup_events, rebuildable.
CREATE TABLE IF NOT EXISTS vocab_entries (
    entry_key            TEXT    PRIMARY KEY,
    kind                 TEXT    NOT NULL,
    lemma                TEXT    NOT NULL,
    target_lang          TEXT    NOT NULL DEFAULT 'ru',
    surface_forms        TEXT    NOT NULL DEFAULT '[]',  -- JSON array of strings
    count                INTEGER NOT NULL DEFAULT 0,
    first_seen           TEXT    NOT NULL,
    last_seen            TEXT    NOT NULL,
    latest_translation   TEXT    NOT NULL DEFAULT '',
    latest_cefr_level    TEXT    NOT NULL DEFAULT '',
    latest_phrase_type   TEXT    NOT NULL DEFAULT '',
    latest_context       TEXT    NOT NULL DEFAULT '',
    latest_article_id    TEXT    NOT NULL DEFAULT '',
    latest_article_title TEXT    NOT NULL DEFAULT '',
    deleted_at           TEXT    NOT NULL DEFAULT '',  -- '' = alive (tombstone otherwise)
    updated_at           TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_vocab_entries_updated_at ON vocab_entries(updated_at);

-- Lemmatizer generation applied to articles.tokens. 0 = never lemmatized; the
-- startup backfill (WORD-CACHE-ARCH.md §4.5) raises it to the current version.
ALTER TABLE articles ADD COLUMN lemma_version INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_articles_lemma_version ON articles(lemma_version);

-- Settings: one toggle governing both vocabulary-aware enrichment and the
-- reader overlay (see §14). On by default.
ALTER TABLE settings ADD COLUMN vocab_assist INTEGER NOT NULL DEFAULT 1;

-- +goose Down

ALTER TABLE settings DROP COLUMN vocab_assist;
DROP INDEX IF EXISTS idx_articles_lemma_version;
ALTER TABLE articles DROP COLUMN lemma_version;
DROP INDEX IF EXISTS idx_vocab_entries_updated_at;
DROP TABLE IF EXISTS vocab_entries;
DROP INDEX IF EXISTS idx_lookup_events_entry;
DROP INDEX IF EXISTS idx_lookup_events_span;
DROP TABLE IF EXISTS lookup_events;
