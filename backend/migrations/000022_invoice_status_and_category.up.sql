-- Status-aware, category-keyed dedup for bulk fakturas. See
-- docs/spec/bulk-faktura-dedup.md for the why.
--
-- - status TEXT: 'open' for live invoices, 'voided' to soft-cancel.
--   The drafts review page filters status='open'; the bulk-faktura
--   dedup index ignores voided rows so a re-issue after voiding
--   is permitted without manual SQL.
-- - category TEXT: denormalized from price_items.category at insert
--   time so the dedup catches the case where a member's beam moves
--   between tiers across years and the admin re-runs the same bulk
--   action under a different price_item_id.

ALTER TABLE invoices ADD COLUMN status TEXT NOT NULL DEFAULT 'open';
ALTER TABLE invoices ADD COLUMN category TEXT;

-- Backfill category for invoices already linked to a price item.
UPDATE invoices i
   SET category = pi.category
  FROM price_items pi
 WHERE i.price_item_id IS NOT NULL
   AND i.price_item_id = pi.id
   AND i.category IS NULL;

DROP INDEX IF EXISTS idx_invoices_bulk_dedup;

-- New dedup: one open invoice per (member, category, fiscal period).
-- Voided invoices fall out of the index so a re-issue is allowed.
CREATE UNIQUE INDEX idx_invoices_bulk_dedup
   ON invoices(club_id, user_id, category, fiscal_period_id)
 WHERE category IS NOT NULL
   AND fiscal_period_id IS NOT NULL
   AND status <> 'voided';

CREATE INDEX idx_invoices_status ON invoices(club_id, status);
