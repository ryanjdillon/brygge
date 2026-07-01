DROP INDEX IF EXISTS idx_users_active;

ALTER TABLE users
  DROP COLUMN IF EXISTS deactivated_by,
  DROP COLUMN IF EXISTS deactivated_at;
