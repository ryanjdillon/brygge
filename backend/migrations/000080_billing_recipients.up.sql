-- Billing recipients: give organization / non-member invoice payers a
-- first-class identity so their payments can be recorded and their invoices
-- marked paid (BRY-234). Until now payments.user_id was NOT NULL, so an invoice
-- with no member (recipient_kind='organization', or a one-off non-member) could
-- never get a payments row — leaving fully GL-reconciled invoices stuck
-- "unpaid" and generating false reminders.
--
-- Org recipient identity was already captured denormalised on each invoice
-- (migration 000034); this normalises it so recurring payers reuse one row and
-- retention has a clean surface (see the PII purge twin in a follow-up).
CREATE TABLE billing_recipients (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id        UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('organization', 'private_nonmember')),
    org_name       TEXT,          -- legal person: not personal data
    org_number     TEXT,
    org_address    TEXT,
    contact_person TEXT,          -- personal data (named individual)
    email          TEXT,          -- personal data if a person's address
    pii_purged_at  TIMESTAMPTZ,   -- set when retention-cleared & scrubbed
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_billing_recipients_club ON billing_recipients(club_id);

-- Payments gain an alternative payer side. A payment is either from a member
-- (user_id) or an external billing recipient (recipient_id) — never both empty.
ALTER TABLE payments ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE payments ADD COLUMN recipient_id UUID REFERENCES billing_recipients(id) ON DELETE SET NULL;
ALTER TABLE payments ADD CONSTRAINT payments_payer_present
    CHECK (user_id IS NOT NULL OR recipient_id IS NOT NULL);

CREATE INDEX idx_payments_recipient ON payments(recipient_id) WHERE recipient_id IS NOT NULL;

-- Invoices link to the reusable recipient (the recipient_org_* columns from
-- 000034 remain as the immutable snapshot for the accounting record).
ALTER TABLE invoices ADD COLUMN recipient_id UUID REFERENCES billing_recipients(id) ON DELETE SET NULL;
