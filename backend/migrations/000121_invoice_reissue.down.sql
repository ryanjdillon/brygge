-- The dev_masked.invoices view depends on the dropped column; the repeatable
-- dev_masked.sql rebuilds it from the current tables on the next migrate run.
DROP VIEW IF EXISTS dev_masked.invoices;

DROP INDEX IF EXISTS idx_invoices_reissued_as;
ALTER TABLE invoices DROP COLUMN IF EXISTS reissued_as_invoice_id;
