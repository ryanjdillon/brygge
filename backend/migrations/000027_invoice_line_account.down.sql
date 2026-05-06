DROP INDEX IF EXISTS idx_invoice_lines_price_item;
DROP INDEX IF EXISTS idx_invoice_lines_account;
ALTER TABLE invoice_lines DROP COLUMN IF EXISTS price_item_id;
ALTER TABLE invoice_lines DROP COLUMN IF EXISTS sub_description;
ALTER TABLE invoice_lines DROP COLUMN IF EXISTS account_id;
