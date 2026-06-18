-- DIL-325: Sparebanken posts cross-bank wires (and some intra-bank
-- transfers) as provisional "Bokført på vei" placeholder rows with no
-- payer info, then later replaces them with a fully-attributed settled
-- row. Because the settled row hashes differently, both land in
-- bank_import_rows and the placeholder lingers as a phantom unmatched
-- entry. Give the treasurer a precise reason to dismiss the stale
-- placeholder — distinct from a plain `duplicate` — so the audit log
-- records that it was superseded by its settled counterpart, without
-- touching any journal entry.
--
-- Idempotent, mirrors 000055: golang-migrate runs each `;` in its own
-- transaction, so the DROP+ADD lives in one DO block.

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
          'test_transaction',
          'superseded'
      ));
END
$$;
