-- +goose Up

-- Collapsed branches of a comment thread, one row per article, synced with LWW
-- on updated_at exactly like `progress`: the reader is offline-first, so a fold
-- made on a phone in the metro has to survive and reach the desktop later.
--
-- `collapsed` is a JSON array of token indices — the index of the first word of
-- the author line the user folded. A token index (rather than a byte offset) is
-- the identity the rest of the reader already uses for a position inside an
-- article, and it survives the byte/UTF-16 divide between the Go tokenizer and
-- the browser.
--
-- ON DELETE CASCADE is what keeps deleting an article from leaving folded-branch
-- rows behind; the client drops its own copy in the same delete.
CREATE TABLE IF NOT EXISTS thread_collapse (
    article_id TEXT PRIMARY KEY REFERENCES articles(id) ON DELETE CASCADE,
    collapsed  TEXT NOT NULL DEFAULT '[]',  -- JSON array of token indices
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_thread_collapse_updated_at ON thread_collapse(updated_at);

-- +goose Down

DROP INDEX IF EXISTS idx_thread_collapse_updated_at;
DROP TABLE IF EXISTS thread_collapse;
