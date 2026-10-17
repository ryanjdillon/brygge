ALTER TABLE bank_import_rows DROP COLUMN IF EXISTS reseen_at;
ALTER TABLE bank_imports DROP COLUMN IF EXISTS period_to;
ALTER TABLE bank_imports DROP COLUMN IF EXISTS period_from;
