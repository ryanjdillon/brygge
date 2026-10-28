-- The dev_masked views depend on these tables; the repeatable dev_masked.sql
-- rebuilds the schema from the current tables on the next migrate run.
DROP VIEW IF EXISTS dev_masked.payment_webhook_events;
DROP VIEW IF EXISTS dev_masked.payment_refunds;

DROP TABLE IF EXISTS payment_webhook_events;
DROP TABLE IF EXISTS payment_refunds;
DROP TABLE IF EXISTS payment_attempts;
