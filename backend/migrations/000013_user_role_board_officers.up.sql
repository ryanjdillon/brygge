-- Extend the user_role enum with board-officer roles. Postgres can't
-- DROP enum values, so the down-migration is intentionally a no-op
-- (the safe rollback is to stop the API from issuing the new values).
--
-- Identifier strings stay English snake_case to match the existing
-- enum convention (harbor_master, treasurer, etc.); display labels
-- are localized in the SPA.

ALTER TYPE user_role ADD VALUE IF NOT EXISTS 'chair';
ALTER TYPE user_role ADD VALUE IF NOT EXISTS 'vice_chair';
ALTER TYPE user_role ADD VALUE IF NOT EXISTS 'deputy';
ALTER TYPE user_role ADD VALUE IF NOT EXISTS 'secretary';
