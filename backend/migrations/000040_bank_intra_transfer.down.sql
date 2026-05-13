DROP INDEX IF EXISTS idx_bank_import_rows_transfer;

ALTER TABLE bank_import_rows
    DROP COLUMN IF EXISTS to_account,
    DROP COLUMN IF EXISTS from_account;

ALTER TABLE bank_imports
    DROP COLUMN IF EXISTS bank_account_number;
