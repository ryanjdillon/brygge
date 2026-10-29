-- Booking Stripe refunds (BRY-446, slice a).
--
-- stripe_balance_transactions:
--   reporting_category gains refund_failure (a refund whose money came back).
--   origin_txn_id is what a non-charge row books against: a refund (and later
--   a dispute or dispute reversal) points at its charge row, a refund_failure
--   at its refund row. order_id and attempt_id stay charge-only.
--   refunded_ore, on a charge row, is what booked refunds took back from it,
--   less booked refund failures. recollected_at, on a charge row, records
--   that a full refund chose to collect again: the charge no longer settles
--   its fakturas (accounting.stripeChargeSettlesSQL).
--   refund_request_key is the key Brygge put in a refund's metadata when it
--   asked for the refund, read from the expanded source at ingest.
--
-- payment_refunds: the provider's ids and failure reason, the request key a
-- refund button sends (unique per club), and the outcome the treasurer chose
-- for a full refund (credit, or recollect; NULL means credit).
--
-- refund_documents: one row per faktura (or order with no faktura) a booked
-- refund touched, and what it did there: a credit note issued (credit), the
-- faktura reopened (recollect), or, for a refund_failure, the debit faktura
-- issued for the money that came back (refail_invoice). credit_invoice_id is
-- the document the row issued (the credit note, or the debit faktura).
-- refund_failure and unassign read these rows instead of re-deriving what a
-- refund did after its faktura was detached from its order.
--
-- clubs.stripe_suspense_account: where a duplicate card capture is parked
-- until its refund books (NULL: 2900).
--
-- invoices (club_id, invoice_number) becomes unique: refunds issue credit
-- notes from the booking pass while a treasurer may be creating fakturas, and
-- every creation path now numbers under a per-club advisory lock.
--
-- Legacy rows: refunds and disputes Brygge held as refund_pending_bry446
-- before refunds were booked become refund_legacy, which nothing retries (a
-- person rebooks or dismisses them); other adjustments held that way are
-- unsupported_category; and refund failures, stored as other_adjustment
-- before refund_failure existed, get their category.

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_constraint
                WHERE conrelid = 'stripe_balance_transactions'::regclass
                  AND conname = 'stripe_balance_transactions_category_check'
                  AND pg_get_constraintdef(oid) NOT LIKE '%refund_failure%') THEN
        ALTER TABLE stripe_balance_transactions DROP CONSTRAINT stripe_balance_transactions_category_check;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                    WHERE conrelid = 'stripe_balance_transactions'::regclass
                      AND conname = 'stripe_balance_transactions_category_check') THEN
        ALTER TABLE stripe_balance_transactions ADD CONSTRAINT stripe_balance_transactions_category_check
            CHECK (reporting_category IN ('charge', 'refund', 'refund_failure', 'dispute', 'dispute_reversal',
                                          'fee', 'payout', 'payout_reversal', 'other_adjustment'));
    END IF;
END
$$;

ALTER TABLE stripe_balance_transactions
    ADD COLUMN IF NOT EXISTS origin_txn_id UUID REFERENCES stripe_balance_transactions(id);
ALTER TABLE stripe_balance_transactions ADD COLUMN IF NOT EXISTS refunded_ore BIGINT NOT NULL DEFAULT 0;
ALTER TABLE stripe_balance_transactions ADD COLUMN IF NOT EXISTS recollected_at TIMESTAMPTZ;
ALTER TABLE stripe_balance_transactions ADD COLUMN IF NOT EXISTS refund_request_key UUID;

CREATE INDEX IF NOT EXISTS idx_stripe_bt_origin
    ON stripe_balance_transactions (origin_txn_id) WHERE origin_txn_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_stripe_bt_club_charge
    ON stripe_balance_transactions (club_id, charge_id) WHERE reporting_category = 'charge';
CREATE INDEX IF NOT EXISTS idx_stripe_bt_club_refund_source
    ON stripe_balance_transactions (club_id, source_id) WHERE reporting_category = 'refund';

ALTER TABLE payment_refunds ADD COLUMN IF NOT EXISTS provider_payment_ref TEXT;
ALTER TABLE payment_refunds ADD COLUMN IF NOT EXISTS charge_ref TEXT;
ALTER TABLE payment_refunds ADD COLUMN IF NOT EXISTS failure_reason TEXT;
ALTER TABLE payment_refunds ADD COLUMN IF NOT EXISTS request_key UUID;
ALTER TABLE payment_refunds ADD COLUMN IF NOT EXISTS outcome TEXT;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                    WHERE conrelid = 'payment_refunds'::regclass AND conname = 'payment_refunds_outcome_check') THEN
        ALTER TABLE payment_refunds ADD CONSTRAINT payment_refunds_outcome_check
            CHECK (outcome IN ('credit', 'recollect'));
    END IF;
END
$$;

CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_refunds_request_key
    ON payment_refunds (club_id, request_key) WHERE request_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS refund_documents (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id           UUID        NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    refund_txn_id     UUID        NOT NULL REFERENCES stripe_balance_transactions(id),
    payment_refund_id UUID        REFERENCES payment_refunds(id) ON DELETE SET NULL,
    invoice_id        UUID        REFERENCES invoices(id),
    order_id          UUID        REFERENCES orders(id),
    credit_invoice_id UUID        REFERENCES invoices(id),
    amount_ore        BIGINT      NOT NULL,
    kind              TEXT        NOT NULL,
    detached_order_id UUID        REFERENCES orders(id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                    WHERE conrelid = 'refund_documents'::regclass AND conname = 'refund_documents_kind_check') THEN
        ALTER TABLE refund_documents ADD CONSTRAINT refund_documents_kind_check
            CHECK (kind IN ('credit', 'recollect', 'refail_invoice'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                    WHERE conrelid = 'refund_documents'::regclass AND conname = 'refund_documents_amount_positive') THEN
        ALTER TABLE refund_documents ADD CONSTRAINT refund_documents_amount_positive
            CHECK (amount_ore > 0);
    END IF;
END
$$;

-- An order with no faktura is one target with a NULL invoice_id, so NULLs
-- must collide too.
CREATE UNIQUE INDEX IF NOT EXISTS idx_refund_documents_txn_invoice
    ON refund_documents (refund_txn_id, invoice_id) NULLS NOT DISTINCT;
CREATE INDEX IF NOT EXISTS idx_refund_documents_invoice
    ON refund_documents (invoice_id) WHERE invoice_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_refund_documents_credit_invoice
    ON refund_documents (credit_invoice_id) WHERE credit_invoice_id IS NOT NULL;

ALTER TABLE clubs ADD COLUMN IF NOT EXISTS stripe_suspense_account TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_invoices_club_number
    ON invoices (club_id, invoice_number);

UPDATE stripe_balance_transactions
   SET status = CASE WHEN status = 'dismissed' THEN status ELSE 'needs_review' END,
       review_reason = 'refund_legacy', updated_at = now()
 WHERE reporting_category IN ('refund', 'dispute', 'dispute_reversal')
   AND review_reason = 'refund_pending_bry446'
   AND journal_entry_id IS NULL;

UPDATE stripe_balance_transactions
   SET review_reason = 'unsupported_category', updated_at = now()
 WHERE reporting_category = 'other_adjustment'
   AND review_reason = 'refund_pending_bry446'
   AND journal_entry_id IS NULL;

UPDATE stripe_balance_transactions
   SET reporting_category = 'refund_failure', review_reason = 'refund_legacy', updated_at = now()
 WHERE reporting_category = 'other_adjustment'
   AND stripe_type = 'refund_failure'
   AND journal_entry_id IS NULL;

-- refund_documents holds only ids, amounts and a kind code, so the
-- read-only dev role reads it raw.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN
        EXECUTE 'GRANT SELECT ON refund_documents TO brygge_dev_ro';
    END IF;
END
$$;
