-- A credit note (an invoice with a negative total) can name the faktura it
-- credits (BRY-342). The link is what settles the pair: the credited faktura
-- counts as settled for the member (finance.SettledForMemberSQL), and a
-- credit note is never a debt. Links are made explicitly by an admin, never
-- inferred, because several open fakturas can share a payer and amount.
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS credits_invoice_id UUID REFERENCES invoices(id);

-- Only credit notes carry the link.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'invoices_credit_link_is_credit_note') THEN
        ALTER TABLE invoices ADD CONSTRAINT invoices_credit_link_is_credit_note
            CHECK (credits_invoice_id IS NULL OR total_amount < 0);
    END IF;
END $$;

-- A faktura is credited by at most one live credit note.
CREATE UNIQUE INDEX IF NOT EXISTS idx_invoices_credits_once
    ON invoices (credits_invoice_id)
    WHERE credits_invoice_id IS NOT NULL AND status <> 'voided';
