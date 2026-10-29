-- The dev_masked views depend on these columns; the repeatable dev_masked.sql
-- rebuilds the schema from the current tables on the next migrate run.
DROP VIEW IF EXISTS dev_masked.stripe_balance_transactions;
DROP VIEW IF EXISTS dev_masked.payment_refunds;
DROP VIEW IF EXISTS dev_masked.clubs;

DROP INDEX IF EXISTS idx_invoices_club_number;
ALTER TABLE clubs DROP COLUMN IF EXISTS stripe_suspense_account;

DROP TABLE IF EXISTS refund_documents;

DROP INDEX IF EXISTS idx_payment_refunds_request_key;
ALTER TABLE payment_refunds DROP CONSTRAINT IF EXISTS payment_refunds_outcome_check;
ALTER TABLE payment_refunds DROP COLUMN IF EXISTS outcome;
ALTER TABLE payment_refunds DROP COLUMN IF EXISTS request_key;
ALTER TABLE payment_refunds DROP COLUMN IF EXISTS failure_reason;
ALTER TABLE payment_refunds DROP COLUMN IF EXISTS charge_ref;
ALTER TABLE payment_refunds DROP COLUMN IF EXISTS provider_payment_ref;

-- Refund failures go back to being stored as other adjustments, and the
-- refund reasons this migration introduced back to the one they replaced.
UPDATE stripe_balance_transactions
   SET reporting_category = 'other_adjustment', review_reason = 'unsupported_category', updated_at = now()
 WHERE reporting_category = 'refund_failure';
UPDATE stripe_balance_transactions
   SET status = CASE WHEN status = 'booked' THEN status ELSE 'needs_review' END,
       review_reason = 'refund_pending_bry446', updated_at = now()
 WHERE reporting_category IN ('refund', 'dispute', 'dispute_reversal')
   AND journal_entry_id IS NULL
   AND status <> 'dismissed';

DROP INDEX IF EXISTS idx_stripe_bt_club_refund_source;
DROP INDEX IF EXISTS idx_stripe_bt_club_charge;
DROP INDEX IF EXISTS idx_stripe_bt_origin;
ALTER TABLE stripe_balance_transactions DROP COLUMN IF EXISTS refund_request_key;
ALTER TABLE stripe_balance_transactions DROP COLUMN IF EXISTS recollected_at;
ALTER TABLE stripe_balance_transactions DROP COLUMN IF EXISTS refunded_ore;
ALTER TABLE stripe_balance_transactions DROP COLUMN IF EXISTS origin_txn_id;

ALTER TABLE stripe_balance_transactions DROP CONSTRAINT IF EXISTS stripe_balance_transactions_category_check;
ALTER TABLE stripe_balance_transactions ADD CONSTRAINT stripe_balance_transactions_category_check
    CHECK (reporting_category IN ('charge', 'refund', 'dispute', 'dispute_reversal', 'fee',
                                  'payout', 'payout_reversal', 'other_adjustment'));
