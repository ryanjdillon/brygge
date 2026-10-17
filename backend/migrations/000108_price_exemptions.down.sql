-- The dev_masked views select the new columns; drop them first so the
-- column drops succeed. The repeatable file recreates them after migrate.
DROP VIEW IF EXISTS dev_masked.invoice_lines;
DROP VIEW IF EXISTS dev_masked.clubs;
ALTER TABLE invoice_lines DROP COLUMN IF EXISTS exemption_role;
ALTER TABLE clubs DROP COLUMN IF EXISTS exemption_expense_account;
DROP TABLE IF EXISTS price_exemptions;
