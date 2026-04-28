DROP INDEX IF EXISTS idx_slips_map_finger_id;

ALTER TABLE slips
    DROP CONSTRAINT IF EXISTS slips_map_side_check,
    DROP COLUMN IF EXISTS map_side,
    DROP COLUMN IF EXISTS map_finger_id,
    DROP COLUMN IF EXISTS map_rotation;

DROP INDEX IF EXISTS idx_dock_fingers_club_id;
DROP TABLE IF EXISTS dock_fingers;
