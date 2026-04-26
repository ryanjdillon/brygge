-- Reverse DIL-227. full_name is untouched and remains authoritative.
ALTER TABLE users
  DROP COLUMN IF EXISTS first_name,
  DROP COLUMN IF EXISTS last_name;
