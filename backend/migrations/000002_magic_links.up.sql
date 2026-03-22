CREATE TABLE magic_links (
    token       TEXT PRIMARY KEY,
    email       TEXT NOT NULL,
    club_id     UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    used        BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_magic_links_email ON magic_links (email, created_at DESC);
CREATE INDEX idx_magic_links_expires_at ON magic_links (expires_at) WHERE used = false;
