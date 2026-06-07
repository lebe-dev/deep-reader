-- +goose Up

-- content_format records the structural format of original_text so the reader
-- can decide how to render it: "plain" (the default — render the token stream as
-- prose) or "markdown" (render Markdown structure — headings, lists, blockquotes,
-- emphasis, code, tables — while keeping words interactive).
--
-- URL-ingested articles are always "plain": the extractor strips Markdown to
-- clean prose before tokenizing. Pasted text (AddText) is stored verbatim, so it
-- is classified at ingest and may be "markdown". Articles created before this
-- column existed default to "plain", matching their pre-existing rendering.
ALTER TABLE articles ADD COLUMN content_format TEXT NOT NULL DEFAULT 'plain';

-- +goose Down

ALTER TABLE articles DROP COLUMN content_format;
