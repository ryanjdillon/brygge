CREATE TABLE dock_fingers (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    label       TEXT NOT NULL DEFAULT '',
    x1          NUMERIC NOT NULL,
    y1          NUMERIC NOT NULL,
    x2          NUMERIC NOT NULL,
    y2          NUMERIC NOT NULL,
    width_m     NUMERIC,
    position    INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_dock_fingers_club_id ON dock_fingers(club_id);

ALTER TABLE slips
    ADD COLUMN map_rotation  NUMERIC NOT NULL DEFAULT 0,
    ADD COLUMN map_finger_id UUID REFERENCES dock_fingers(id) ON DELETE SET NULL,
    ADD COLUMN map_side      TEXT;

ALTER TABLE slips
    ADD CONSTRAINT slips_map_side_check
    CHECK (map_side IS NULL OR map_side IN ('port', 'starboard'));

CREATE INDEX idx_slips_map_finger_id ON slips(map_finger_id);
