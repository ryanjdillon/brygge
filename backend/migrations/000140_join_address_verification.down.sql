DROP TRIGGER IF EXISTS users_clear_stale_address_verification ON users;
DROP FUNCTION IF EXISTS users_clear_stale_address_verification();

ALTER TABLE membership_applications DROP COLUMN IF EXISTS address_verified_at;
ALTER TABLE membership_applications DROP COLUMN IF EXISTS address_kommunenummer;
ALTER TABLE membership_applications DROP COLUMN IF EXISTS address_verified;

ALTER TABLE users DROP COLUMN IF EXISTS address_verified_at;
ALTER TABLE users DROP COLUMN IF EXISTS address_kommunenummer;
ALTER TABLE users DROP COLUMN IF EXISTS address_verified;
