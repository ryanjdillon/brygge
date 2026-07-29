ALTER TABLE invoices DROP COLUMN IF EXISTS recipient_id;

DROP INDEX IF EXISTS idx_payments_recipient;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_payer_present;
ALTER TABLE payments DROP COLUMN IF EXISTS recipient_id;
-- Restoring NOT NULL is unsafe if recipient-only payments exist; leave user_id
-- nullable on rollback (harmless — the app still requires a payer via app code).

DROP TABLE IF EXISTS billing_recipients;
