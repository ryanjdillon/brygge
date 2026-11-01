-- BRY-554 / ACC-13: the schema for booking without a human.
--
-- users.kind separates people from the club's system actor, the principal the
-- automatic run books as. journal_entries.created_by stays NOT NULL, so every
-- "who did it" column keeps pointing at a real users row. The actor row itself
-- is not inserted here: accounting.Service.SystemActor creates it on first use
-- per club (PLAT-1: nothing is seeded per club by a migration). No code path
-- changes kind after the row is created, so nothing guards an UPDATE of it.
ALTER TABLE users ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'person';

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'users_kind_check') THEN
    ALTER TABLE users ADD CONSTRAINT users_kind_check CHECK (kind IN ('person', 'system'));
  END IF;
END $$;

-- One system actor per club. SystemActor's INSERT names this index as its
-- conflict target, so a concurrent first call is a no-op.
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_one_system_per_club
    ON users (club_id) WHERE kind = 'system';

-- Backstop: the system actor can never hold a role, so it can never count as
-- an admin (the last-admin guard) or a board member (the board listing).
CREATE OR REPLACE FUNCTION user_roles_refuse_system_user() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND kind = 'system') THEN
    RAISE EXCEPTION 'the system user cannot hold a role' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS user_roles_refuse_system_user ON user_roles;
CREATE TRIGGER user_roles_refuse_system_user
    BEFORE INSERT OR UPDATE ON user_roles
    FOR EACH ROW EXECUTE FUNCTION user_roles_refuse_system_user();

-- Master switch for unattended booking. Off for every club until its
-- treasurer or admin turns it on (PLAT-1): order-linked booking and the payout
-- sweep had no switch before, and running them unattended is new behaviour.
ALTER TABLE clubs ADD COLUMN IF NOT EXISTS automatic_booking BOOLEAN NOT NULL DEFAULT false;

-- Set when a person undoes a booking or clearing, so that row leaves every
-- automatic pass until a person books it again. Only the columns live here;
-- the passes that set and honour them come with their own changes.
ALTER TABLE vipps_import_rows ADD COLUMN IF NOT EXISTS auto_declined_at TIMESTAMPTZ;
ALTER TABLE bank_import_rows  ADD COLUMN IF NOT EXISTS auto_declined_at TIMESTAMPTZ;
