-- Single fakturas can be addressed to an external organisation
-- instead of the member personally. The User record stays linked
-- (so the contact person still sees the invoice in their portal),
-- but the PDF "Til:" block, the recipient address, and the email
-- destination override the member's personal details.
--
-- 'organization' covers both frivillig (non-profit) and ordinary
-- bedrift recipients — the bookkeeping shape is the same for either
-- and the org_number alone is enough for the recipient's accounting
-- system to match. Keeping this as a single flag avoids a needless
-- branch today; if a future feature needs the distinction we can add
-- a sub-type column without breaking the base.

ALTER TABLE invoices ADD COLUMN recipient_kind            TEXT NOT NULL DEFAULT 'private'
    CHECK (recipient_kind IN ('private', 'organization'));
ALTER TABLE invoices ADD COLUMN recipient_org_name        TEXT;
ALTER TABLE invoices ADD COLUMN recipient_org_number      TEXT;
ALTER TABLE invoices ADD COLUMN recipient_org_address     TEXT;
ALTER TABLE invoices ADD COLUMN recipient_contact_person  TEXT;
ALTER TABLE invoices ADD COLUMN recipient_their_ref       TEXT;
ALTER TABLE invoices ADD COLUMN recipient_email           TEXT;
