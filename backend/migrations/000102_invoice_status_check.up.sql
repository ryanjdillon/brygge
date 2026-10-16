-- invoices.status was free TEXT. The only values the code writes are 'open'
-- and 'voided' (verified against prod before adding this, BRY-325); a typo
-- such as 'void' or 'paid' would silently make an invoice count as unpaid
-- and get chased. Payment state lives in payment_id / vipps_paid_at, not here.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'invoices_status_check'
           AND conrelid = 'invoices'::regclass
    ) THEN
        ALTER TABLE invoices
          ADD CONSTRAINT invoices_status_check
          CHECK (status IN ('open', 'voided'));
    END IF;
END $$;
