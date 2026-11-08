-- The dev_masked.clubs view reads the dropped column, so it goes first.
-- Rebuilding it needs the matching dev_masked.sql too: the current file names
-- public_description, so revert the code with the schema before the next
-- migrate run applies it.
DROP VIEW IF EXISTS dev_masked.clubs;

ALTER TABLE clubs DROP COLUMN IF EXISTS public_description;
