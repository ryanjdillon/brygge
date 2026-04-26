-- DIL-28: drop columns and tables that supported the removed Vipps Login
-- and JWT auth paths. After this migration:
--   * Identity: email-only (magic-link login + cookie sessions)
--   * No password storage (no password_hash)
--   * No JWT refresh-token revocation table
--
-- The unique constraint that referenced vipps_sub also goes; nothing else
-- in production references it.

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_club_id_vipps_sub_key;
ALTER TABLE users DROP COLUMN IF EXISTS password_hash;
ALTER TABLE users DROP COLUMN IF EXISTS vipps_sub;

DROP INDEX IF EXISTS idx_revoked_tokens_expires;
DROP TABLE IF EXISTS revoked_tokens;
