-- Add a permanent/seasonal distinction to slip_assignments. Existing
-- rows are treated as 'permanent' (the prior implicit semantic of an
-- active assignment).

CREATE TYPE slip_assignment_type AS ENUM (
    'permanent',
    'seasonal'
);

ALTER TABLE slip_assignments
    ADD COLUMN assignment_type slip_assignment_type NOT NULL DEFAULT 'permanent';

CREATE INDEX idx_slip_assignments_type
    ON slip_assignments(club_id, assignment_type)
    WHERE released_at IS NULL;
