BEGIN;

CREATE TABLE broadcasts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    subject     TEXT NOT NULL,
    body        TEXT NOT NULL,
    recipients  TEXT NOT NULL,
    sent_by     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sent_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_broadcasts_club_id ON broadcasts(club_id);
CREATE INDEX idx_broadcasts_sent_at ON broadcasts(club_id, sent_at);

COMMIT;
