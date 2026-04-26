-- DIL-227: split users.full_name into first_name + last_name.
-- Phase 1 of the migration sequence (DIL-226):
--   1. ADD columns + backfill (this file)
--   2. Switch full_name to a generated column, dual-write from handlers (DIL-228)
--   3. Migrate readers + UI (DIL-229)
--   4. DROP full_name (DIL-230)
--
-- Existing handlers keep working: they still read/write full_name. The new
-- columns are populated for every existing row so admin tooling can sort
-- and personalise immediately.

ALTER TABLE users
  ADD COLUMN first_name TEXT NOT NULL DEFAULT '',
  ADD COLUMN last_name  TEXT NOT NULL DEFAULT '';

-- Naive split: everything before the last space goes into first_name; the
-- final token becomes last_name. Single-token names (e.g. mononyms) land
-- entirely in first_name with last_name empty. Admins can correct outliers
-- in-app once DIL-229 ships the editor UI.
UPDATE users SET
  first_name = CASE
    WHEN position(' ' in full_name) > 0
      THEN trim(substring(full_name from 1
            for length(full_name) - position(' ' in reverse(full_name))))
    ELSE full_name
  END,
  last_name = CASE
    WHEN position(' ' in full_name) > 0
      THEN trim(substring(full_name
            from length(full_name) - position(' ' in reverse(full_name)) + 2))
    ELSE ''
  END
WHERE first_name = '' AND last_name = '';
