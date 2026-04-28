-- Harbor map: GeoJSON-based layout. Slip positions and dock fingers are
-- stored as GeoJSON geometry in WGS84 (lon/lat) so the map can render
-- on real chart tiles (Kartverket sjøkart) and survive replacement of
-- the local outline. JSONB rather than PostGIS to keep the dev image
-- (postgres:16-alpine) dependency-free; we can adopt PostGIS later if
-- spatial queries become useful.

CREATE TABLE dock_fingers (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    geometry    JSONB NOT NULL,
    position    INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT dock_fingers_geometry_is_linestring
        CHECK (geometry->>'type' = 'LineString'
               AND jsonb_typeof(geometry->'coordinates') = 'array')
);

CREATE INDEX idx_dock_fingers_club_id ON dock_fingers(club_id);

ALTER TABLE slips
    ADD COLUMN location JSONB,
    ADD CONSTRAINT slips_location_is_point
        CHECK (location IS NULL
               OR (location->>'type' = 'Point'
                   AND jsonb_typeof(location->'coordinates') = 'array'));
