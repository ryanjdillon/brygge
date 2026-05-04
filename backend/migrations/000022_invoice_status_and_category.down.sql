DROP INDEX IF EXISTS idx_invoices_status;
DROP INDEX IF EXISTS idx_invoices_bulk_dedup;
ALTER TABLE invoices DROP COLUMN IF EXISTS category;
ALTER TABLE invoices DROP COLUMN IF EXISTS status;

-- Restore the original price-item-keyed dedup so a downgrade still
-- prevents the simplest duplicate case.
CREATE UNIQUE INDEX idx_invoices_bulk_dedup
   ON invoices(club_id, user_id, price_item_id, fiscal_period_id)
 WHERE price_item_id IS NOT NULL AND fiscal_period_id IS NOT NULL;
