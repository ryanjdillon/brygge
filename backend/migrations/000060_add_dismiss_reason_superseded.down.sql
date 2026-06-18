-- Reverses 000060. NULL out any superseded dismissals first (fall back
-- to `unidentifiable`) since dropping the value from the CHECK without
-- clearing those rows would leave the table violating the new (narrower)
-- constraint.

UPDATE bank_import_rows
SET dismissed_reason = 'unidentifiable'
WHERE dismissed_reason = 'superseded';

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
