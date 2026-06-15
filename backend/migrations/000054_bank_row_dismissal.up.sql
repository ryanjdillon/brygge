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

-- Every DDL statement here is idempotent (IF NOT EXISTS / pg_constraint
-- guard) so the migration is safe to retry after a partial failure.
-- golang-migrate's pgx driver runs each ";" as its own transaction —
-- so a retry must not error on already-applied steps.

ALTER TABLE bank_import_rows
  ADD COLUMN IF NOT EXISTS dismissed_at      TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS dismissed_by      UUID REFERENCES users(id),
  ADD COLUMN IF NOT EXISTS dismissed_reason  TEXT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'bank_import_rows_dismissed_reason_check'
    ) THEN
        ALTER TABLE bank_import_rows
          ADD CONSTRAINT bank_import_rows_dismissed_reason_check
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
    END IF;
END
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'bank_import_rows_dismissal_consistency'
    ) THEN
        ALTER TABLE bank_import_rows
          ADD CONSTRAINT bank_import_rows_dismissal_consistency CHECK (
            (dismissed_at IS NULL AND dismissed_by IS NULL AND dismissed_reason IS NULL)
            OR
            (dismissed_at IS NOT NULL AND dismissed_by IS NOT NULL AND dismissed_reason IS NOT NULL)
          );
    END IF;
END
$$;

CREATE INDEX IF NOT EXISTS idx_bank_import_rows_unmatched
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
