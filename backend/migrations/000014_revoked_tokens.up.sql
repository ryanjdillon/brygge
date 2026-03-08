CREATE TABLE revoked_tokens (
    jti         TEXT PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id),
    revoked_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_revoked_tokens_expires ON revoked_tokens(expires_at);
