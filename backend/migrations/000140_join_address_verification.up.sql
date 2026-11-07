-- Address verification on join (BRY-639). The join form looks the home
-- address up in Kartverket's address register; the server re-looks up the
-- picked address, stores the register's street, postal code and city, and
-- derives is_local from its municipality (kommunenummer) instead of asking.
--
-- address_verified: TRUE when the stored address came from the register
-- (address_kommunenummer and address_verified_at are then set), FALSE when it
-- was typed by hand (the register was skipped or unreachable): is_local is
-- then unknown and stored false for the board to settle. NULL on users means
-- the address predates verification or was entered by the board.
--
-- is_local stays NOT NULL: many readers scan it into a Go bool, and
-- address_verified = false already says "unknown".
ALTER TABLE users ADD COLUMN IF NOT EXISTS address_verified BOOLEAN;
ALTER TABLE users ADD COLUMN IF NOT EXISTS address_kommunenummer TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS address_verified_at TIMESTAMPTZ;

ALTER TABLE membership_applications ADD COLUMN IF NOT EXISTS address_verified BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE membership_applications ADD COLUMN IF NOT EXISTS address_kommunenummer TEXT;
ALTER TABLE membership_applications ADD COLUMN IF NOT EXISTS address_verified_at TIMESTAMPTZ;

-- A verification describes one address. Any write that changes a verified
-- user's street, postal code or city without also writing a new verification
-- (a profile edit, an admin edit, a CSV import) leaves an address the
-- register never saw, so the verification is cleared rather than left to
-- vouch for it. The WHEN clause keeps every other users update from calling
-- the function at all.
CREATE OR REPLACE FUNCTION users_clear_stale_address_verification() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.address_verified IS NOT DISTINCT FROM OLD.address_verified
     AND NEW.address_verified_at IS NOT DISTINCT FROM OLD.address_verified_at THEN
    NEW.address_verified := NULL;
    NEW.address_kommunenummer := NULL;
    NEW.address_verified_at := NULL;
  END IF;
  RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS users_clear_stale_address_verification ON users;
CREATE TRIGGER users_clear_stale_address_verification
    BEFORE UPDATE ON users
    FOR EACH ROW
    WHEN (OLD.address_verified IS TRUE
          AND (OLD.address_line, OLD.postal_code, OLD.city)
              IS DISTINCT FROM (NEW.address_line, NEW.postal_code, NEW.city))
    EXECUTE FUNCTION users_clear_stale_address_verification();
