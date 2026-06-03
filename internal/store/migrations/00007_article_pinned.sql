-- +goose Up

-- pinned marks an article the user wants kept at the top of the library. It is a
-- plain user flag (not part of the enrichment pipeline) synced like the rest of
-- the article metadata: toggling it bumps articles.updated_at so the delta-sync
-- cursor (?since=) carries the change to the client on the next pull.
ALTER TABLE articles ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0; -- SQLite boolean: 0/1

-- +goose Down

ALTER TABLE articles DROP COLUMN pinned;
