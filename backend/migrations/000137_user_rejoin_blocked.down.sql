-- The dev_masked.users view depends on the column; the repeatable
-- dev_masked.sql rebuilds it from the current tables on the next migrate run.
DROP VIEW IF EXISTS dev_masked.users;

ALTER TABLE users DROP COLUMN IF EXISTS rejoin_blocked;
