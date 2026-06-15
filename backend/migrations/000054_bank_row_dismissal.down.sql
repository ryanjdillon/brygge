-- Reverses 000054_bank_row_dismissal. Drops the dismissal-related
-- columns + constraints + index. Cannot reconstitute the prior
-- kid_number values that the data-normalisation UPDATE cleared —
-- that's a one-way operation by design (the cleared values were junk
-- that blocked KID auto-match in the first place).

DROP INDEX IF EXISTS idx_bank_import_rows_unmatched;

ALTER TABLE bank_import_rows
  DROP CONSTRAINT IF EXISTS bank_import_rows_dismissal_consistency;

ALTER TABLE bank_import_rows
  DROP COLUMN IF EXISTS dismissed_reason,
  DROP COLUMN IF EXISTS dismissed_by,
  DROP COLUMN IF EXISTS dismissed_at;
