-- dev_masked.clubs selects bank_auto_assign, so the column can't be dropped
-- while the view exists. The repeatable dev_masked.sql rebuilds the view from
-- the current tables on the next migrate run.
DROP VIEW IF EXISTS dev_masked.clubs;
ALTER TABLE clubs DROP COLUMN IF EXISTS bank_auto_assign;
