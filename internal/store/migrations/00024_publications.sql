-- +goose Up

-- Publications: a share link that exposes one article's translated text on a
-- public, unauthenticated page. The token IS the capability — it is the whole
-- secret, so it is generated from a CSPRNG and is long enough not to be
-- guessable, while the row's expires_at bounds how long the link works.
--
-- One publication per article (the unique index): re-publishing an already
-- published article replaces the previous link rather than accumulating tokens.
--
-- The rendered page itself lives on disk (PUBLIC_PAGES_DIR/<token>.html), so
-- serving it needs no template rendering per request; this table is only the
-- gate that decides whether the file may still be handed out.
CREATE TABLE IF NOT EXISTS publications (
    token        TEXT PRIMARY KEY,
    article_id   TEXT NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
    title        TEXT NOT NULL DEFAULT '',
    description  TEXT NOT NULL DEFAULT '',
    published_at TEXT NOT NULL,
    expires_at   TEXT NOT NULL DEFAULT ''  -- RFC3339; '' means no expiry
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_publications_article_id ON publications(article_id);
CREATE INDEX IF NOT EXISTS idx_publications_expires_at ON publications(expires_at);

-- public_page_ttl_hours is how long a freshly published link stays reachable,
-- in hours. It is read at publish time and stamped into the row, so changing it
-- never retroactively shortens or extends links already handed out.
ALTER TABLE settings ADD COLUMN public_page_ttl_hours INTEGER NOT NULL DEFAULT 72;

-- +goose Down

ALTER TABLE settings DROP COLUMN public_page_ttl_hours;

DROP INDEX IF EXISTS idx_publications_expires_at;
DROP INDEX IF EXISTS idx_publications_article_id;
DROP TABLE IF EXISTS publications;
