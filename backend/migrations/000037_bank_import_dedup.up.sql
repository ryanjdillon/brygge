-- Add club_id (denormalized for unique index scope) and row_hash to bank_import_rows
-- so re-imports of the same statement skip rows that already exist.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE bank_import_rows
    ADD COLUMN club_id  UUID,
    ADD COLUMN row_hash CHAR(64);

-- Backfill club_id from the parent bank_imports row
UPDATE bank_import_rows bir
SET club_id = bi.club_id
FROM bank_imports bi
WHERE bir.bank_import_id = bi.id;

-- Backfill row_hash for existing rows using the same recipe as the application code.
-- Recipe: sha256(lower(date || '|' || amount || '|' || reference || '|' || description || '|' || counterpart))
UPDATE bank_import_rows
SET row_hash = encode(
    public.digest(
        lower(
            to_char(row_date, 'YYYY-MM-DD') || '|' ||
            trim(to_char(amount, 'FM999999999990.00')) || '|' ||
            COALESCE(reference, '') || '|' ||
            COALESCE(description, '') || '|' ||
            COALESCE(counterpart, '')
        ),
        'sha256'
    ),
    'hex'
);

ALTER TABLE bank_import_rows
    ALTER COLUMN club_id  SET NOT NULL,
    ALTER COLUMN row_hash SET NOT NULL;

CREATE UNIQUE INDEX idx_bank_import_rows_dedup
    ON bank_import_rows(club_id, row_hash);
