-- The dev_masked.clubs view depends on the dropped column; the repeatable
-- dev_masked.sql rebuilds it from the current tables on the next migrate run.
DROP VIEW IF EXISTS dev_masked.clubs;

ALTER TABLE clubs DROP COLUMN IF EXISTS vipps_portal_url;
