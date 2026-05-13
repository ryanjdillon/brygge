DROP INDEX IF EXISTS idx_bank_import_rows_dedup;

ALTER TABLE bank_import_rows
    DROP COLUMN IF EXISTS row_hash,
    DROP COLUMN IF EXISTS club_id;
