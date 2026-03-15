-- 000017_normalize_identifiers.down.sql
-- Reverse all identifier renames back to Norwegian/British English.

BEGIN;

-- Column renames (reverse)
ALTER TABLE clubs RENAME COLUMN required_volunteer_hours TO required_dugnad_hours;
ALTER TABLE tasks RENAME COLUMN responsible_id TO ansvarlig_id;
ALTER TABLE slip_assignments RENAME COLUMN harbor_membership_amount TO andel_amount;
ALTER TABLE slip_assignments RENAME COLUMN harbor_membership_paid_at TO andel_paid_at;

-- Enum renames (reverse)
ALTER TYPE payment_type RENAME VALUE 'harbor_membership' TO 'andel';
ALTER TYPE resource_type RENAME VALUE 'motorhome_spot' TO 'bobil_spot';
ALTER TYPE event_tag RENAME VALUE 'volunteer' TO 'dugnad';
ALTER TYPE document_visibility RENAME VALUE 'board' TO 'styre';
ALTER TYPE user_role RENAME VALUE 'slip_holder' TO 'slip_owner';
ALTER TYPE user_role RENAME VALUE 'harbor_master' TO 'harbour_master';
ALTER TYPE user_role RENAME VALUE 'board' TO 'styre';

COMMIT;
