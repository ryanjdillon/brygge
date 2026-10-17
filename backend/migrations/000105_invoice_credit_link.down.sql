DROP INDEX IF EXISTS idx_invoices_credits_once;
ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_credit_link_is_credit_note;
ALTER TABLE invoices DROP COLUMN IF EXISTS credits_invoice_id;
