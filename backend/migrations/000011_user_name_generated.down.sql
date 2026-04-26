-- Reverse DIL-228. Materialise the generated value back into a plain
-- column so handlers can write to it again, then DIL-227's down migration
-- can drop first_name + last_name cleanly if needed.

ALTER TABLE users DROP COLUMN full_name;
ALTER TABLE users ADD COLUMN full_name TEXT NOT NULL DEFAULT '';

UPDATE users SET full_name = trim(first_name || ' ' || last_name);
