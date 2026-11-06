-- The dev_masked views read the dropped columns and table, so they go first.
-- Rebuilding them needs the matching dev_masked.sql too: the current file
-- names these columns and the membership_applications table, so revert the
-- code with the schema before the next migrate run applies it.
DROP VIEW IF EXISTS dev_masked.users;
DROP VIEW IF EXISTS dev_masked.clubs;
DROP VIEW IF EXISTS dev_masked.orders;
DROP VIEW IF EXISTS dev_masked.invoices;
DROP VIEW IF EXISTS dev_masked.membership_applications;

ALTER TABLE invoices DROP CONSTRAINT IF EXISTS invoices_purpose_check;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_purpose_check;
ALTER TABLE invoices DROP COLUMN IF EXISTS purpose;
ALTER TABLE orders DROP COLUMN IF EXISTS purpose;

ALTER TABLE clubs DROP COLUMN IF EXISTS join_price_item_id;

DROP TABLE IF EXISTS membership_applications;

ALTER TABLE users DROP COLUMN IF EXISTS membership_cancel_reason;
ALTER TABLE users DROP COLUMN IF EXISTS membership_ends_on;
ALTER TABLE users DROP COLUMN IF EXISTS membership_cancelled_at;
ALTER TABLE users DROP COLUMN IF EXISTS member_since;
