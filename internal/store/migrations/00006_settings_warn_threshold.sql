-- +goose Up

-- markdown_warn_threshold: when today's remaining markdown.new conversions drop
-- to this number or below, the client shows a prominent low-budget banner.
-- 0 disables the warning entirely. Added to the settings singleton so the
-- threshold syncs across the user's devices like the other reading preferences.
ALTER TABLE settings ADD COLUMN markdown_warn_threshold INTEGER NOT NULL DEFAULT 5;

-- +goose Down

ALTER TABLE settings DROP COLUMN markdown_warn_threshold;
