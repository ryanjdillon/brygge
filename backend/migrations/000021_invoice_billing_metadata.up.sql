-- Track which price item and fiscal period an invoice was generated
-- for. Lets the bulk-fakturas flow skip duplicate sends and the
-- accounting view filter by period.

ALTER TABLE invoices ADD COLUMN price_item_id UUID REFERENCES price_items(id) ON DELETE SET NULL;
ALTER TABLE invoices ADD COLUMN fiscal_period_id UUID REFERENCES fiscal_periods(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX idx_invoices_bulk_dedup
   ON invoices(club_id, user_id, price_item_id, fiscal_period_id)
 WHERE price_item_id IS NOT NULL AND fiscal_period_id IS NOT NULL;
