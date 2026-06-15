DROP INDEX IF EXISTS idx_bank_import_rows_invoice_id;
ALTER TABLE bank_import_rows DROP COLUMN IF EXISTS invoice_id;
