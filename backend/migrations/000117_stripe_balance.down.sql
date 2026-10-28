-- The dev_masked views depend on these; the repeatable dev_masked.sql
-- rebuilds the schema from the current tables on the next migrate run.
DROP VIEW IF EXISTS dev_masked.stripe_balance_transactions;
DROP VIEW IF EXISTS dev_masked.clubs;

ALTER TABLE clubs DROP COLUMN IF EXISTS stripe_payout_descriptor;
DROP TABLE IF EXISTS stripe_sync_state;
DROP TABLE IF EXISTS stripe_balance_transactions;
DROP TABLE IF EXISTS stripe_payouts;
