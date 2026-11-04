-- The dev_masked views read these tables; the repeatable dev_masked.sql
-- rebuilds the schema from the current tables on the next migrate run.
DROP VIEW IF EXISTS dev_masked.mail_delivery_events;
DROP VIEW IF EXISTS dev_masked.email_sends;

DROP TABLE IF EXISTS mail_delivery_events;
DROP TABLE IF EXISTS email_sends;
