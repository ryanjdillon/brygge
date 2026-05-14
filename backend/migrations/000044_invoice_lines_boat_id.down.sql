DROP INDEX IF EXISTS idx_invoice_lines_boat;
ALTER TABLE invoice_lines DROP COLUMN IF EXISTS boat_id;
