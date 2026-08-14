DROP TABLE IF EXISTS webauthn_credentials;
DROP INDEX IF EXISTS idx_users_webauthn_handle;
ALTER TABLE users DROP COLUMN IF EXISTS webauthn_handle;
