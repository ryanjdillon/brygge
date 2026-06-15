-- Track the origin of historically-imported invoices so they can be
-- identified in the UI and de-duplicated on re-import.
ALTER TABLE invoices
    ADD COLUMN import_source TEXT,
    ADD COLUMN external_id   TEXT;

-- Allow imported invoices to have no KID (they were issued by another system).
ALTER TABLE invoices ALTER COLUMN kid_number DROP NOT NULL;

-- Rebuild the kid uniqueness index as a partial so NULLs and empty
-- strings (import placeholders) are excluded.
DROP INDEX IF EXISTS idx_invoices_kid;
CREATE UNIQUE INDEX idx_invoices_kid
    ON invoices (club_id, kid_number)
    WHERE kid_number IS NOT NULL AND kid_number <> '';

-- One invoice per (source, external reference) per club.
CREATE UNIQUE INDEX idx_invoices_external
    ON invoices (club_id, import_source, external_id)
    WHERE import_source IS NOT NULL AND external_id IS NOT NULL;
