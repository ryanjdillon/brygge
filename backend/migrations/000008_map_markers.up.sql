CREATE TABLE map_markers (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    marker_type TEXT NOT NULL,
    label       TEXT NOT NULL DEFAULT '',
    lat         NUMERIC NOT NULL,
    lng         NUMERIC NOT NULL,
    sort_order  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_map_markers_club ON map_markers(club_id);
