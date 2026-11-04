-- The dev_masked.users view depends on the dropped columns; the repeatable
-- dev_masked.sql rebuilds it from the current tables on the next migrate run.
DROP VIEW IF EXISTS dev_masked.users;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_harbor_share_paid_on_requires_owner;
ALTER TABLE users DROP COLUMN IF EXISTS harbor_share_paid_on;
ALTER TABLE users DROP COLUMN IF EXISTS harbor_share_owner;
