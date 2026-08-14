-- Passkeys / WebAuthn (BRY-248). A member may register one or more passkeys,
-- used as a phishing-resistant step-up factor (in place of TOTP) and, later,
-- as an optional login method. Magic-link login is unchanged.

-- Opaque, non-PII WebAuthn user handle (stable per account, <=64 bytes).
-- Never derived from email/name so it can't be correlated to a person.
ALTER TABLE users ADD COLUMN IF NOT EXISTS webauthn_handle BYTEA;
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_webauthn_handle
    ON users (webauthn_handle) WHERE webauthn_handle IS NOT NULL;

CREATE TABLE webauthn_credentials (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    club_id         UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    credential_id   BYTEA NOT NULL,
    -- Full go-webauthn Credential as JSON — the source of truth that round-trips
    -- the library's nested struct (flags, authenticator, attestation) intact.
    credential      JSONB NOT NULL,
    -- Mirrors credential.authenticator.signCount, updated on each assertion for
    -- clone detection. Extracted for cheap inspection.
    sign_count      BIGINT NOT NULL DEFAULT 0,
    aaguid          BYTEA,
    transports      TEXT[] NOT NULL DEFAULT '{}',
    backup_eligible BOOLEAN NOT NULL DEFAULT false,
    backup_state    BOOLEAN NOT NULL DEFAULT false,
    name            TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at    TIMESTAMPTZ
);

CREATE UNIQUE INDEX idx_webauthn_credentials_credential_id
    ON webauthn_credentials (credential_id);
CREATE INDEX idx_webauthn_credentials_user
    ON webauthn_credentials (user_id);
