-- Auto-assignment of unambiguous Vipps charges (BRY-340).
--
-- clubs.vipps_auto_assign: opt-in. Off by default so no club has charges
-- booked without having chosen it.
-- clubs.vipps_auto_assign_tie_category: the price-item category to use when a
-- charge amount fits active price items in more than one category (e.g. a
-- guest berth and a motorhome spot at the same per-day price). NULL leaves
-- such ties for a human.
-- vipps_import_rows.invoice_id: the faktura a charge settled, so unassigning
-- can reopen exactly that faktura.
-- vipps_import_rows.auto_assign_rule: which rule booked the charge
-- ('member_invoice' or 'price_item'); NULL for manual bookings.
ALTER TABLE clubs ADD COLUMN IF NOT EXISTS vipps_auto_assign BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE clubs ADD COLUMN IF NOT EXISTS vipps_auto_assign_tie_category TEXT;
ALTER TABLE vipps_import_rows ADD COLUMN IF NOT EXISTS invoice_id UUID REFERENCES invoices(id) ON DELETE SET NULL;
ALTER TABLE vipps_import_rows ADD COLUMN IF NOT EXISTS auto_assign_rule TEXT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'vipps_import_rows_auto_assign_rule_check'
           AND conrelid = 'vipps_import_rows'::regclass
    ) THEN
        ALTER TABLE vipps_import_rows
          ADD CONSTRAINT vipps_import_rows_auto_assign_rule_check
          CHECK (auto_assign_rule IS NULL OR auto_assign_rule IN ('member_invoice', 'price_item'));
    END IF;
END $$;
