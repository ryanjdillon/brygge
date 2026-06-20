DROP INDEX IF EXISTS idx_bank_import_rows_refund_pending;

ALTER TABLE bank_import_rows
  DROP COLUMN IF EXISTS refund_paired_with;
