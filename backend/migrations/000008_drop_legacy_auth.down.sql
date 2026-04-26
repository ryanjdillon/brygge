-- Recreate the columns and revoked-tokens table. Restoring data is out
-- of scope; existing users will simply have NULL password_hash/vipps_sub
-- and no revoked-token history.

ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS vipps_sub TEXT;
ALTER TABLE users ADD CONSTRAINT users_club_id_vipps_sub_key UNIQUE (club_id, vipps_sub);

CREATE TABLE IF NOT EXISTS revoked_tokens (
    jti        TEXT PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    revoked_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_revoked_tokens_expires ON revoked_tokens(expires_at);
