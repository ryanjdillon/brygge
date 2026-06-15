-- 000058 made kid_number nullable and the Uni24 import stored NULL for
-- invoices that have no KID. But the rest of the codebase scans
-- kid_number into a non-nullable Go string, so those NULLs poisoned the
-- invoice-suggestion queries (pgx surfaces the scan failure via
-- rows.Err(), yielding a 400 on bank-row reconciliation).
--
-- Represent "no KID" as '' instead of NULL and restore the NOT NULL
-- invariant. The uniqueness index stays partial so multiple '' rows
-- (imported invoices) don't collide.
UPDATE invoices SET kid_number = '' WHERE kid_number IS NULL;

ALTER TABLE invoices ALTER COLUMN kid_number SET NOT NULL;

DROP INDEX IF EXISTS idx_invoices_kid;
CREATE UNIQUE INDEX IF NOT EXISTS idx_invoices_kid
    ON invoices (club_id, kid_number)
    WHERE kid_number <> '';
