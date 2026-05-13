-- Capture Fra konto / Til konto so internal transfers between two of the
-- club's own bank accounts can be detected and posted as a single bilag.

ALTER TABLE bank_imports
    ADD COLUMN bank_account_number TEXT NOT NULL DEFAULT '';

ALTER TABLE bank_import_rows
    ADD COLUMN from_account TEXT NOT NULL DEFAULT '',
    ADD COLUMN to_account   TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_bank_import_rows_transfer
    ON bank_import_rows(club_id, row_date, amount)
    WHERE journal_entry_id IS NULL AND (from_account <> '' OR to_account <> '');
