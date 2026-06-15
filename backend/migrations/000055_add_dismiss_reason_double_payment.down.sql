-- Reverses 000055. NULL out any existing double_payment dismissals
-- first, since dropping the value from the CHECK without clearing
-- those rows would leave the table in a state that violates the new
-- (narrower) constraint. We can't recover what reason they "should
-- have been" — fall back to `unidentifiable`.

UPDATE bank_import_rows
SET dismissed_reason = 'unidentifiable'
WHERE dismissed_reason = 'double_payment';

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'bank_import_rows_dismissed_reason_check'
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
          'bank_fee',
          'refund_or_credit',
          'overpayment',
          'unidentifiable',
          'test_transaction'
      ));
END
$$;
