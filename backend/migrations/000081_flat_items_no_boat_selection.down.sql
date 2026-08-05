ALTER TABLE price_items ALTER COLUMN requires_boat_selection SET DEFAULT TRUE;

-- Restore migration 000028's post-backfill state: flat items required a
-- boat except the two batch categories that auto-resolve it from a slip.
UPDATE price_items
   SET requires_boat_selection = TRUE
 WHERE pricing_kind = 'flat'
   AND category NOT IN ('harbor_membership', 'slip_fee');
