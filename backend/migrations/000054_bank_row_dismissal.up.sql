-- DIL-392 Phase 1: per-row bank reconciliation UI.
--
-- Two pieces of schema needed by the new "Tildel" tab on the Bank
-- page:
--   1. Dismissal columns on bank_import_rows so operators can mark
--      rows as not-actionable with a structured reason. Hidden from
--      the queue afterwards but kept in the audit trail.
--   2. Partial index on rows that still need reconciling — i.e.
--      neither journaled nor dismissed. The badge query and the
--      Tildel-tab list both pivot on this predicate.
--
-- Reasons are constrained to a fixed enum (CHECK rather than a real
-- enum type so future additions are migration-only — opening a text
-- box for "other" would let operators write `sldkjfgsldkjjs` into the
-- audit log).

ALTER TABLE bank_import_rows
  ADD COLUMN dismissed_at      TIMESTAMPTZ,
  ADD COLUMN dismissed_by      UUID REFERENCES users(id),
  ADD COLUMN dismissed_reason  TEXT
    CHECK (dismissed_reason IS NULL OR dismissed_reason IN (
        'bounced',
        'internal_transfer',
        'duplicate',
        'bank_fee',
        'refund_or_credit',
        'overpayment',
        'unidentifiable',
        'test_transaction'
    ));

-- All three columns travel together: either none set (still pending),
-- or all three set (dismissed). Saves a state-shape bug down the line.
ALTER TABLE bank_import_rows
  ADD CONSTRAINT bank_import_rows_dismissal_consistency CHECK (
    (dismissed_at IS NULL AND dismissed_by IS NULL AND dismissed_reason IS NULL)
    OR
    (dismissed_at IS NOT NULL AND dismissed_by IS NOT NULL AND dismissed_reason IS NOT NULL)
  );

CREATE INDEX idx_bank_import_rows_unmatched
  ON bank_import_rows (club_id, row_date DESC)
  WHERE journal_entry_id IS NULL AND dismissed_at IS NULL;

-- One-time normalisation: clear shape-invalid kid_number values that
-- the Sparebank Melding column carried into unreconciled rows. These
-- are free-text strings ("Medlemskontingent Olav Alpen", "Faktura 44",
-- ".") that block KID-based auto-match because the `WHERE kid_number
-- = $kid` predicate never matches a real invoice KID. Validation in
-- `banksync.go::isBryggeKID` already filters at sync-time going
-- forward; this catches the existing stuck rows so the new Tildel
-- queue starts with clean data.
--
-- Luhn validation stays Go-side. We only shape-filter here (length 12
-- + all digits). The next `Run sync` re-runs ExtractKIDFromDescription
-- + Luhn against the description for the cleared rows.
UPDATE bank_import_rows
SET kid_number = ''
WHERE journal_entry_id IS NULL
  AND kid_number IS NOT NULL
  AND kid_number <> ''
  AND NOT (length(kid_number) = 12 AND kid_number ~ '^[0-9]{12}$');
