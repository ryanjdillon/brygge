-- Per-club bank account registry with semantic roles, replacing the
-- single clubs.bank_account TEXT field. The faktura PDF picks the
-- row flagged is_default_for_invoices; statement uploads will match
-- against account_number (DIL-342). DIL-338/339.
--
-- clubs.bank_account is kept for one release as a fallback for code
-- not yet migrated; it is dropped in a follow-up migration once
-- DIL-340/341 have landed.

CREATE TABLE club_bank_accounts (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id                  UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    account_number           TEXT NOT NULL,
    role                     TEXT NOT NULL
        CHECK (role IN ('drift','hoyrente','other')),
    gl_code                  TEXT NOT NULL DEFAULT '1920',
    label                    TEXT,
    is_default_for_invoices  BOOLEAN NOT NULL DEFAULT FALSE,
    archived_at              TIMESTAMPTZ,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Account numbers must be unique within a club among live (non-archived) rows.
CREATE UNIQUE INDEX club_bank_accounts_club_number_live_idx
    ON club_bank_accounts (club_id, account_number)
    WHERE archived_at IS NULL;

-- Exactly one default-for-invoices account per club among live rows.
CREATE UNIQUE INDEX club_bank_accounts_one_default_idx
    ON club_bank_accounts (club_id)
    WHERE is_default_for_invoices AND archived_at IS NULL;

CREATE INDEX club_bank_accounts_club_idx
    ON club_bank_accounts (club_id)
    WHERE archived_at IS NULL;

-- Backfill: one row per club whose clubs.bank_account is non-empty,
-- tagged as drift and marked as the faktura default. Preserves the
-- exact string (formatting, separators) so reissued fakturas match
-- already-sent PDFs.
INSERT INTO club_bank_accounts (club_id, account_number, role, is_default_for_invoices)
SELECT id, bank_account, 'drift', TRUE
FROM clubs
WHERE bank_account IS NOT NULL
  AND btrim(bank_account) <> '';
