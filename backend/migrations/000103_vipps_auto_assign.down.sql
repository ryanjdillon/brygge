ALTER TABLE vipps_import_rows DROP CONSTRAINT IF EXISTS vipps_import_rows_auto_assign_rule_check;
ALTER TABLE vipps_import_rows DROP COLUMN IF EXISTS auto_assign_rule;
ALTER TABLE vipps_import_rows DROP COLUMN IF EXISTS invoice_id;
ALTER TABLE clubs DROP COLUMN IF EXISTS vipps_auto_assign_tie_category;
ALTER TABLE clubs DROP COLUMN IF EXISTS vipps_auto_assign;
