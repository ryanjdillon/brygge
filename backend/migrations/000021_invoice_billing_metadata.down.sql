DROP INDEX IF EXISTS idx_invoices_bulk_dedup;
ALTER TABLE invoices DROP COLUMN IF EXISTS fiscal_period_id;
ALTER TABLE invoices DROP COLUMN IF EXISTS price_item_id;
