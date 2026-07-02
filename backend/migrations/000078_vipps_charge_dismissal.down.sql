DROP INDEX IF EXISTS idx_vipps_import_rows_open_charges;

ALTER TABLE vipps_import_rows
  DROP COLUMN IF EXISTS dismissed_at,
  DROP COLUMN IF EXISTS dismissed_reason,
  DROP COLUMN IF EXISTS dismissed_by;
