-- "Ny faktura" after a card refund (BRY-498). When a refund credited a whole
-- faktura, the treasurer can issue a fresh faktura for the same lines to
-- collect again. invoices.reissued_as_invoice_id, on the credited faktura,
-- names the faktura that was issued; it is what makes the action once-only
-- (a second request finds it set, under the faktura's row lock). The unique
-- index keeps one new faktura from standing in for two credited ones. Deleting
-- the new faktura (a draft that was never sent) clears the link, and a new
-- faktura that was voided no longer blocks issuing another.

ALTER TABLE invoices ADD COLUMN IF NOT EXISTS reissued_as_invoice_id UUID REFERENCES invoices(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_invoices_reissued_as
    ON invoices (reissued_as_invoice_id) WHERE reissued_as_invoice_id IS NOT NULL;
