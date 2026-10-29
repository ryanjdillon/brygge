-- The dev_masked.clubs view depends on the dropped column; the repeatable
-- dev_masked.sql rebuilds it from the current tables on the next migrate run.
DROP VIEW IF EXISTS dev_masked.clubs;

-- Account 7830 is left in the charts: a club may already have posted to it.

-- Disputes go back to being held for a person.
UPDATE stripe_balance_transactions
   SET status = 'needs_review', review_reason = 'refund_legacy', updated_at = now()
 WHERE reporting_category IN ('dispute', 'dispute_reversal')
   AND journal_entry_id IS NULL
   AND status IN ('unbooked', 'needs_review')
   AND (review_reason IS NULL OR review_reason = 'dispute_account_missing')
   AND currency = 'nok';

ALTER TABLE clubs DROP COLUMN IF EXISTS stripe_dispute_account;

DROP TABLE IF EXISTS payment_disputes;
