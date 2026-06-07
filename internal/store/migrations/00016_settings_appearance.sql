-- +goose Up

-- Reader appearance preferences, editable from the Settings > Appearance tab and
-- synced across the user's devices like other settings.
--
-- font_size:   reader text size preset — one of 's' | 'm' | 'l' | 'xl'.
-- line_height: reader line-spacing preset — one of 'compact' | 'normal' | 'relaxed'.
--
-- Defaults ('m' / 'normal') reproduce the previously hard-coded reader typography
-- (17px / 1.8 line-height on desktop), so existing rows keep their look.
ALTER TABLE settings ADD COLUMN font_size   TEXT NOT NULL DEFAULT 'm';
ALTER TABLE settings ADD COLUMN line_height TEXT NOT NULL DEFAULT 'normal';

-- +goose Down

ALTER TABLE settings DROP COLUMN line_height;
ALTER TABLE settings DROP COLUMN font_size;
