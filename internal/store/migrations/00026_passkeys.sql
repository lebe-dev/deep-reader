-- +goose Up

-- The single built-in account gains a stable, opaque WebAuthn user handle. It
-- is the value returned as PublicKeyCredentialUserEntity.id during registration
-- and echoed back by the authenticator as userHandle on a discoverable login,
-- so it must never change once a passkey exists. randomblob(32) generates it in
-- the same statement that creates the account, which keeps ports.Store's
-- CreateUser signature free of a handle argument.
ALTER TABLE app_user ADD COLUMN webauthn_user_handle BLOB;
UPDATE app_user SET webauthn_user_handle = randomblob(32) WHERE webauthn_user_handle IS NULL;

-- webauthn_credentials: the passkeys registered against the built-in account.
--
-- credential_id is the raw WebAuthn credential ID as sent by the authenticator;
-- it is UNIQUE because a discoverable login looks the credential up by exactly
-- this value. `credential` holds the JSON-marshalled webauthn.Credential (public
-- key, transports, flags, sign counter, attestation) — keeping the library's own
-- struct as the storage format means a library upgrade that adds a field does
-- not need a migration, and nothing here is re-derived by hand.
--
-- id is a ULID used only in the REST paths, so renaming/deleting a passkey never
-- has to put a raw credential ID in a URL.
CREATE TABLE IF NOT EXISTS webauthn_credentials (
    id            TEXT PRIMARY KEY,
    credential_id BLOB NOT NULL UNIQUE,
    name          TEXT NOT NULL,
    credential    TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    last_used_at  TEXT
);

CREATE INDEX IF NOT EXISTS idx_webauthn_credentials_created_at
    ON webauthn_credentials (created_at);

-- +goose Down

DROP INDEX IF EXISTS idx_webauthn_credentials_created_at;
DROP TABLE IF EXISTS webauthn_credentials;
ALTER TABLE app_user DROP COLUMN webauthn_user_handle;
