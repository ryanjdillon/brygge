-- dev_masked.invoices selects credits_invoice_id, so the column can't be
-- dropped while the view exists. The repeatable dev_masked.sql rebuilds the
-- whole schema from the current tables on the next migrate run.
DROP VIEW IF EXISTS dev_masked.invoices;
DROP INDEX IF EXISTS idx_invoices_credits_once;
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_credit_link_is_credit_note;
ALTER TABLE invoices DROP COLUMN IF EXISTS credits_invoice_id;
