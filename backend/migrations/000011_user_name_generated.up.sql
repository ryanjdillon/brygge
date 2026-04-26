-- DIL-228: convert users.full_name to a generated column.
--
-- After this migration, full_name is computed from first_name + last_name
-- and cannot be written to directly. All handler/seed writes must populate
-- first_name + last_name instead — see commit history for the cutover.
--
-- Postgres requires DROP-then-ADD to retype an existing column as
-- GENERATED. Indexes on full_name (none today) would need to be dropped
-- before this and re-created after.

ALTER TABLE users DROP COLUMN full_name;

ALTER TABLE users ADD COLUMN full_name TEXT
  GENERATED ALWAYS AS (
    NULLIF(trim(first_name || ' ' || last_name), '')
  ) STORED;
