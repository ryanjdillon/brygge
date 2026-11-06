-- The dev_masked.email_sends view reads these columns; the repeatable
-- dev_masked.sql rebuilds it from the current tables on the next migrate run.
DROP VIEW IF EXISTS dev_masked.email_sends;

DROP INDEX IF EXISTS idx_email_sends_lower_recipient;
DROP INDEX IF EXISTS idx_email_sends_stalwart_open;
ALTER TABLE email_sends DROP COLUMN IF EXISTS outcome_final_at;
ALTER TABLE email_sends DROP COLUMN IF EXISTS outcome_checked_at;
ALTER TABLE email_sends DROP COLUMN IF EXISTS outcome_detail;
