BEGIN;

-- Backfill where exactly one boat exists for the slip-assignment owner.
UPDATE slip_assignments sa
   SET boat_id = b.id
  FROM boats b
 WHERE sa.boat_id IS NULL
   AND sa.released_at IS NULL
   AND b.user_id = sa.user_id
   AND b.club_id = sa.club_id
   AND (SELECT count(*) FROM boats b2
         WHERE b2.user_id = sa.user_id AND b2.club_id = sa.club_id) = 1;

-- Hard fail if any active assignment is still missing a boat.
DO $$
DECLARE n int;
BEGIN
  SELECT count(*) INTO n FROM slip_assignments
   WHERE released_at IS NULL AND boat_id IS NULL;
  IF n > 0 THEN
    RAISE EXCEPTION
      'cannot tighten boat_id: % active assignments have no boat. '
      'Resolve them manually first.', n;
  END IF;
END $$;

ALTER TABLE slip_assignments
  ADD CONSTRAINT active_assignment_has_boat
  CHECK (released_at IS NOT NULL OR boat_id IS NOT NULL);

CREATE UNIQUE INDEX idx_slip_assignments_active_boat
   ON slip_assignments(boat_id) WHERE released_at IS NULL;

-- Keep slip_assignments.user_id denormalized; trigger keeps it in sync
-- with boats.user_id so existing read paths don't have to change.
CREATE OR REPLACE FUNCTION sync_slip_assignment_user() RETURNS TRIGGER AS $$
BEGIN
  IF NEW.boat_id IS NOT NULL THEN
    SELECT user_id INTO NEW.user_id FROM boats WHERE id = NEW.boat_id;
  END IF;
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER trg_slip_assignment_user
  BEFORE INSERT OR UPDATE OF boat_id ON slip_assignments
  FOR EACH ROW EXECUTE FUNCTION sync_slip_assignment_user();

COMMIT;
