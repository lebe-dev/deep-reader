-- +goose Up

-- source_type records what kind of content the article holds so the library can
-- tell an ordinary article apart from a discussion thread: "article" (the
-- default — a page fetched and extracted as prose) or "comments" (a comment
-- thread pulled from a discussion site such as Hacker News and rendered as
-- Markdown).
--
-- It is set by the fetch stage from the extractor that produced the content, so
-- articles created before this column existed default to "article", matching
-- their existing rendering and library filtering.
ALTER TABLE articles ADD COLUMN source_type TEXT NOT NULL DEFAULT 'article';

-- +goose Down

ALTER TABLE articles DROP COLUMN source_type;
