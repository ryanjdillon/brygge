CREATE TABLE user_consents (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    club_id     UUID NOT NULL REFERENCES clubs(id),
    consent_type TEXT NOT NULL,
    version     TEXT NOT NULL,
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ
);
CREATE INDEX idx_user_consents_user ON user_consents(user_id);

CREATE TABLE deletion_requests (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id),
    club_id         UUID NOT NULL REFERENCES clubs(id),
    requested_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    grace_end       TIMESTAMPTZ NOT NULL,
    cancelled_at    TIMESTAMPTZ,
    processed_at    TIMESTAMPTZ,
    status          TEXT NOT NULL DEFAULT 'pending'
);

CREATE TABLE legal_documents (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id),
    doc_type    TEXT NOT NULL,
    version     TEXT NOT NULL,
    content     TEXT NOT NULL,
    published_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
