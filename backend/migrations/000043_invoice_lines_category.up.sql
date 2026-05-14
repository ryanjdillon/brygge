-- Per-line category for accurate dedup + admin chip lighting on
-- multi-line invoices.
--
-- The invoices.category column (added in 000022) is a single-valued
-- denormalization from the line's price_item.category, which means a
-- multi-line invoice carrying both 'harbor_membership' and 'slip_fee'
-- can only claim ONE category in the dedup index — leaving the user
-- unprotected against being re-billed for the other line's category.
-- Denormalizing onto invoice_lines fixes that.
--
-- The dedup remains enforced at insert time in the bulk-faktura handler
-- because a DB-level unique constraint across the join would require a
-- helper table or trigger. The handler check is updated in tandem.

ALTER TABLE invoice_lines ADD COLUMN category TEXT;

UPDATE invoice_lines il
   SET category = pi.category
  FROM price_items pi
 WHERE il.price_item_id = pi.id
   AND il.category IS NULL;

CREATE INDEX idx_invoice_lines_category ON invoice_lines(category) WHERE category IS NOT NULL;
