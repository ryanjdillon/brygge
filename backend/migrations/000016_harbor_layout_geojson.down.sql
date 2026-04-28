ALTER TABLE slips
    DROP CONSTRAINT IF EXISTS slips_location_is_point,
    DROP COLUMN IF EXISTS location;

DROP INDEX IF EXISTS idx_dock_fingers_club_id;
DROP TABLE IF EXISTS dock_fingers;
