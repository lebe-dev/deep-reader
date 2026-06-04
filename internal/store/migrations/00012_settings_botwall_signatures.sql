-- +goose Up

-- bot_wall_signatures: the user's custom newline-separated list of bot-wall /
-- captcha substrings the fetch stage matches against to detect a challenge page
-- (Cloudflare, Vercel Security Checkpoint, …) before any LLM call, editable from
-- the Reading settings tab. An empty string means "use the built-in default
-- list" (model.DefaultBotWallSignatures). Stored on the settings singleton so it
-- syncs across the user's devices like other settings.
ALTER TABLE settings ADD COLUMN bot_wall_signatures TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE settings DROP COLUMN bot_wall_signatures;
