-- 000017_normalize_identifiers.up.sql
-- Rename Norwegian and British English identifiers to American English.
--
-- Enum renames (PostgreSQL ≥ 10):
--   ALTER TYPE ... RENAME VALUE 'old' TO 'new'
--
-- Column renames:
--   ALTER TABLE ... RENAME COLUMN old TO new

BEGIN;

-- =============================================================================
-- 1. user_role enum: styre→board, harbour_master→harbor_master, slip_owner→slip_holder
-- =============================================================================

ALTER TYPE user_role RENAME VALUE 'styre' TO 'board';
ALTER TYPE user_role RENAME VALUE 'harbour_master' TO 'harbor_master';
ALTER TYPE user_role RENAME VALUE 'slip_owner' TO 'slip_holder';

-- =============================================================================
-- 2. document_visibility enum: styre→board
-- =============================================================================

ALTER TYPE document_visibility RENAME VALUE 'styre' TO 'board';

-- =============================================================================
-- 3. event_tag enum: dugnad→volunteer
-- =============================================================================

ALTER TYPE event_tag RENAME VALUE 'dugnad' TO 'volunteer';

-- =============================================================================
-- 4. resource_type enum: bobil_spot→motorhome_spot
-- =============================================================================

ALTER TYPE resource_type RENAME VALUE 'bobil_spot' TO 'motorhome_spot';

-- =============================================================================
-- 5. payment_type enum: andel→harbor_membership
-- =============================================================================

ALTER TYPE payment_type RENAME VALUE 'andel' TO 'harbor_membership';

-- =============================================================================
-- 6. Column renames: slip_assignments
-- =============================================================================

ALTER TABLE slip_assignments RENAME COLUMN andel_amount TO harbor_membership_amount;
ALTER TABLE slip_assignments RENAME COLUMN andel_paid_at TO harbor_membership_paid_at;

-- =============================================================================
-- 7. Column renames: tasks (dugnad tracking — ansvarlig_id→assignee_id already
--    exists in the original init migration for project tasks; the dugnad migration
--    000007 added a *second* ansvarlig_id. Rename that one.)
-- =============================================================================

ALTER TABLE tasks RENAME COLUMN ansvarlig_id TO responsible_id;

-- =============================================================================
-- 8. Column renames: clubs (dugnad→volunteer)
-- =============================================================================

ALTER TABLE clubs RENAME COLUMN required_dugnad_hours TO required_volunteer_hours;

COMMIT;
