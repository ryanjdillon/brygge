-- DIL-207: per-user TOTP recovery codes.
--
-- Each row is a single-use bcrypt hash. Plaintext codes are returned
-- to the user once at enrollment (or regeneration) and never again.
-- Marking used_at consumes the code; the row stays for the audit trail
-- (and to make replay attacks visibly fail).

CREATE TABLE totp_recovery_codes (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, code_hash)
);

CREATE INDEX idx_totp_recovery_codes_user ON totp_recovery_codes(user_id) WHERE used_at IS NULL;
