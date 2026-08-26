-- Restore NOT NULL. Errors if any automated (NULL imported_by) imports exist;
-- delete or backfill them before rolling back.
ALTER TABLE vipps_imports ALTER COLUMN imported_by SET NOT NULL;
