-- dev_masked.invoices and dev_masked.clubs select the new columns, so they
-- can't be dropped while the views exist. The repeatable dev_masked.sql
-- rebuilds the views from the current tables on the next migrate run.
DROP VIEW IF EXISTS dev_masked.invoices;
DROP VIEW IF EXISTS dev_masked.clubs;

ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_online_paid_provider_set;
ALTER TABLE invoices DROP COLUMN IF EXISTS online_payment_opt_in;
ALTER TABLE invoices DROP COLUMN IF EXISTS online_paid_provider;
ALTER TABLE invoices DROP COLUMN IF EXISTS online_paid_at;

ALTER TABLE clubs DROP COLUMN IF EXISTS stripe_clearing_account;
ALTER TABLE clubs DROP COLUMN IF EXISTS payment_fee_account;
ALTER TABLE clubs DROP COLUMN IF EXISTS online_max_amount;

-- Account 1580 stays: it may already carry journal lines. A club that wants
-- it gone should deactivate it after moving its postings.
