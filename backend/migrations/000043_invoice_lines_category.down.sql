DROP INDEX IF EXISTS idx_invoice_lines_category;
ALTER TABLE invoice_lines DROP COLUMN IF EXISTS category;
