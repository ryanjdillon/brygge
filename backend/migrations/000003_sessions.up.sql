CREATE TABLE sessions (
    id               TEXT PRIMARY KEY,
    user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    club_id          UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    expires_at       TIMESTAMPTZ NOT NULL,
    totp_verified_at TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ip_address       INET,
    user_agent       TEXT
);

CREATE INDEX idx_sessions_user_id ON sessions (user_id);
CREATE INDEX idx_sessions_expires_at ON sessions (expires_at);
