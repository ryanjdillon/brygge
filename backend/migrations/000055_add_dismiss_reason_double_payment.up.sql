-- DIL-394 follow-up: distinguish "true duplicate" (same arkivref —
-- same bank booking surfaced twice in different exports) from "double
-- payment" (different arkivref — member paid the right amount twice).
-- The two need different operator handling: duplicate dismisses
-- silently; double payment requires a refund to the member.
--
-- Until the full refund workflow lands (separate follow-up), the
-- operator marks the second row with the new `double_payment` reason
-- so the audit log reflects intent.
--
-- Idempotent: replaces the CHECK constraint only if it doesn't yet
-- include `double_payment`. golang-migrate runs each `;` in its own
-- transaction so we cannot rely on an inline DROP+ADD safely.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'bank_import_rows_dismissed_reason_check'
           AND conrelid = 'bank_import_rows'::regclass
    ) THEN
        ALTER TABLE bank_import_rows
          DROP CONSTRAINT bank_import_rows_dismissed_reason_check;
    END IF;

    ALTER TABLE bank_import_rows
      ADD CONSTRAINT bank_import_rows_dismissed_reason_check
      CHECK (dismissed_reason IS NULL OR dismissed_reason IN (
          'bounced',
          'internal_transfer',
          'duplicate',
          'double_payment',
          'bank_fee',
          'refund_or_credit',
          'overpayment',
          'unidentifiable',
          'test_transaction'
      ));
END
$$;
