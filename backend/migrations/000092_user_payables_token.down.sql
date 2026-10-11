DROP INDEX IF EXISTS idx_users_payables_token;
ALTER TABLE users DROP COLUMN IF EXISTS payables_token;
