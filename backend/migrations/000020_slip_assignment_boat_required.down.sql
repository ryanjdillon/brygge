BEGIN;

DROP TRIGGER IF EXISTS trg_slip_assignment_user ON slip_assignments;
DROP FUNCTION IF EXISTS sync_slip_assignment_user();
DROP INDEX IF EXISTS idx_slip_assignments_active_boat;
ALTER TABLE slip_assignments DROP CONSTRAINT IF EXISTS active_assignment_has_boat;

COMMIT;
