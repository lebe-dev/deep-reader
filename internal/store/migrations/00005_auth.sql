-- +goose Up

-- app_user: the single built-in account. We enforce exactly one row via
-- CHECK(id = 1). password_hash holds a bcrypt hash; the plaintext is never
-- stored. The service is considered "initialized" iff this row exists.
CREATE TABLE IF NOT EXISTS app_user (
    id            INTEGER PRIMARY KEY CHECK(id = 1),
    username      TEXT    NOT NULL,
    password_hash TEXT    NOT NULL,
    created_at    TEXT    NOT NULL,
    updated_at    TEXT    NOT NULL
);

-- sessions: active login sessions. token_hash is the SHA-256 of the opaque
-- bearer token handed to the client, so a leak of this table cannot be replayed
-- as a token. created_at is kept for housekeeping/debugging.
CREATE TABLE IF NOT EXISTS sessions (
    token_hash TEXT PRIMARY KEY,
    created_at TEXT NOT NULL
);

-- +goose Down

DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS app_user;
