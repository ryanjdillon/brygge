-- Docks: logical groups of slips with a default map view (center + zoom)
-- so the harbor map can offer "fly to dock" navigation buttons. Linked
-- to slip section names by `slug`.

CREATE TABLE docks (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id       UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    slug          TEXT NOT NULL,
    name          TEXT NOT NULL,
    default_lng   DOUBLE PRECISION,
    default_lat   DOUBLE PRECISION,
    default_zoom  REAL,
    position      INT NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (club_id, slug)
);

CREATE INDEX idx_docks_club_id ON docks(club_id);
