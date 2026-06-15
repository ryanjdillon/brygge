DROP INDEX IF EXISTS idx_invoices_kid;

ALTER TABLE invoices ALTER COLUMN kid_number DROP NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_invoices_kid
    ON invoices (club_id, kid_number)
    WHERE kid_number IS NOT NULL AND kid_number <> '';
