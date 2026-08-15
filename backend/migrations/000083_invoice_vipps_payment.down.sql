DROP INDEX IF EXISTS idx_invoices_vipps_payment_reference;

ALTER TABLE invoices
  DROP COLUMN IF EXISTS vipps_payment_reference,
  DROP COLUMN IF EXISTS vipps_payment_url,
  DROP COLUMN IF EXISTS vipps_payment_expires_at;
