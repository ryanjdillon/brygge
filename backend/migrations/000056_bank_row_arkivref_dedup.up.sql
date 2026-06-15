-- Remove unjournaled duplicate rows where another row with the same
-- (club_id, reference) already exists. Keep the journaled row if one
-- exists; otherwise keep the earliest-created row. Only unjournaled
-- rows are deleted — journaled rows are never touched.
WITH ranked AS (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY club_id, reference
               ORDER BY
                   (journal_entry_id IS NOT NULL) DESC,
                   created_at ASC
           ) AS rn
    FROM bank_import_rows
    WHERE reference <> ''
)
DELETE FROM bank_import_rows
WHERE id IN (SELECT id FROM ranked WHERE rn > 1)
  AND journal_entry_id IS NULL;

-- Enforce Arkivref uniqueness at the DB level so description-reformatted
-- re-imports can never create a second row for the same booking.
CREATE UNIQUE INDEX IF NOT EXISTS idx_bank_import_rows_arkivref
    ON bank_import_rows (club_id, reference)
    WHERE reference <> '';
