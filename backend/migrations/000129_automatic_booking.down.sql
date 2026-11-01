-- The dev_masked views list these tables' columns; the repeatable
-- dev_masked.sql rebuilds them from the current tables on the next migrate run.
DROP VIEW IF EXISTS dev_masked.users;
DROP VIEW IF EXISTS dev_masked.clubs;
DROP VIEW IF EXISTS dev_masked.vipps_import_rows;
DROP VIEW IF EXISTS dev_masked.bank_import_rows;

DROP TRIGGER IF EXISTS user_roles_refuse_system_user ON user_roles;
DROP FUNCTION IF EXISTS user_roles_refuse_system_user();

ALTER TABLE bank_import_rows  DROP COLUMN IF EXISTS auto_declined_at;
ALTER TABLE vipps_import_rows DROP COLUMN IF EXISTS auto_declined_at;
ALTER TABLE clubs DROP COLUMN IF EXISTS automatic_booking;

DROP INDEX IF EXISTS idx_users_one_system_per_club;

-- A system actor that has created a journal entry (or been named in an audit
-- row) cannot be deleted: the foreign keys forbid it, and a bilag's author must
-- stay a real row. Delete the actors nothing references; keep the others.
DO $$
DECLARE
  actor RECORD;
BEGIN
  FOR actor IN SELECT id FROM users WHERE kind = 'system' LOOP
    BEGIN
      DELETE FROM users WHERE id = actor.id;
    EXCEPTION WHEN foreign_key_violation THEN
      NULL;
    END;
  END LOOP;
END $$;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_kind_check;

-- The column goes only when no system actor was kept. If one was, kind stays
-- (without its CHECK) so that row is still told apart from a person, and the
-- up migration picks it up again. Older code does not filter on kind and
-- would list that row as a member until the up migration is applied again.
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM users WHERE kind = 'system') THEN
    ALTER TABLE users DROP COLUMN IF EXISTS kind;
  END IF;
END $$;
