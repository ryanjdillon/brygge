DROP INDEX IF EXISTS idx_slip_assignments_type;
ALTER TABLE slip_assignments DROP COLUMN IF EXISTS assignment_type;
DROP TYPE IF EXISTS slip_assignment_type;
