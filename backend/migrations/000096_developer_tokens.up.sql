-- Developer bearer tokens for the read-only dev-query endpoint (BRY-115).
--
-- An admin mints a short-lived token so an external tool (e.g. an AI agent
-- that cannot use the operator's SSH key) can call POST /api/v1/dev/query.
-- Only sha256(token) is stored; the plaintext is shown once at creation.
-- A 32-byte random token has no low-entropy guess surface, so a fast hash
-- is the right lookup key (a slow KDF per request would be a DoS lever).
--
-- The brygge_dev_ro role is default-deny (000095) and this table is not on
-- its allowlist, so query sessions can never read or enumerate tokens.

CREATE TABLE IF NOT EXISTS developer_tokens (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id       UUID        NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    name          TEXT        NOT NULL,
    token_prefix  TEXT        NOT NULL,
    token_hash    TEXT        NOT NULL,
    scope         TEXT        NOT NULL DEFAULT 'db_query',
    created_by    UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    last_used_at  TIMESTAMPTZ,
    revoked_at    TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS developer_tokens_hash ON developer_tokens(token_hash);
CREATE        INDEX IF NOT EXISTS developer_tokens_club ON developer_tokens(club_id, created_at DESC);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'developer_tokens_scope_check') THEN
        ALTER TABLE developer_tokens
            ADD CONSTRAINT developer_tokens_scope_check CHECK (scope IN ('db_query'));
    END IF;
END
$$;
