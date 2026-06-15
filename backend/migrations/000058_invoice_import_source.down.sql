DROP INDEX IF EXISTS idx_invoices_external;

DROP INDEX IF EXISTS idx_invoices_kid;
CREATE UNIQUE INDEX idx_invoices_kid ON invoices (club_id, kid_number);

ALTER TABLE invoices ALTER COLUMN kid_number SET NOT NULL;

ALTER TABLE invoices
    DROP COLUMN IF EXISTS import_source,
    DROP COLUMN IF EXISTS external_id;
