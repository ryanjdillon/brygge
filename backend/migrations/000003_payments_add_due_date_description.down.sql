DROP INDEX IF EXISTS idx_payments_due_date;
ALTER TABLE payments DROP COLUMN IF EXISTS description;
ALTER TABLE payments DROP COLUMN IF EXISTS due_date;
