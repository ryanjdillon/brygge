-- Stripe disputes (BRY-446, slice b).
--
-- payment_disputes: one row per dispute Stripe reports against a card
-- payment, kept up to date by the Stripe webhook (the only writer) so that
-- the treasurer can be told, a refund can be refused while the dispute is
-- open, and an inbox can list what is waiting for evidence. It never holds
-- booking state: the loss and its reversal are booked from the dispute's
-- balance transactions by the Stripe booking pass. status is Stripe's own
-- word (needs_response, under_review, won, lost, ...), so it is free text.
-- is_charge_refundable is Stripe's own answer to whether the payment can
-- still be refunded (NULL when an event left it out). notified_phase is the
-- latest phase the treasurer was told about (1 an inquiry, 2 a chargeback),
-- so each phase is told once however often it is delivered.
-- attempt_id and order_id are NULL for a dispute on a payment Brygge cannot
-- tie to an attempt.
--
-- clubs.stripe_dispute_account: the expense account a lost dispute is booked
-- to (NULL: 7830 Tap på fordringer).
--
-- Account 7830 is added to the charts of clubs seeded before it joined the
-- default kontoplan (as migration 000099 did for 3130, 3210 and 3220), so a
-- lost dispute can book on a club that never touched its chart.
--
-- Legacy rows: disputes held as refund_legacy by migration 000119, before
-- disputes were booked, go back to the booking pass, unless their payout was
-- already cleared in the books (that clearing counted them as done) or they
-- are not in NOK. Rows a person dismissed stay dismissed.

CREATE TABLE IF NOT EXISTS payment_disputes (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id              UUID        NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    attempt_id           UUID        REFERENCES payment_attempts(id),
    order_id             UUID        REFERENCES orders(id),
    provider             TEXT        NOT NULL,
    provider_dispute_id  TEXT        NOT NULL,
    charge_ref           TEXT,
    provider_payment_ref TEXT,
    amount               NUMERIC,
    currency             TEXT        NOT NULL DEFAULT 'NOK',
    reason               TEXT        NOT NULL DEFAULT '',
    status               TEXT        NOT NULL DEFAULT '',
    evidence_due_by      TIMESTAMPTZ,
    is_charge_refundable BOOLEAN,
    notified_phase       SMALLINT    NOT NULL DEFAULT 0,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_disputes_provider_dispute
    ON payment_disputes (provider, provider_dispute_id);
CREATE INDEX IF NOT EXISTS idx_payment_disputes_attempt
    ON payment_disputes (attempt_id) WHERE attempt_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_payment_disputes_club_status
    ON payment_disputes (club_id, status);

ALTER TABLE clubs ADD COLUMN IF NOT EXISTS stripe_dispute_account TEXT;

INSERT INTO accounts (club_id, code, name, account_type, parent_code, is_system, is_active, mva_eligible, description, sort_order)
SELECT c.id, '7830', 'Tap på fordringer', 'expense', '7000', true, true, 'ineligible',
       'Tapt kortbetalingstvist', 330
FROM clubs c
WHERE EXISTS (SELECT 1 FROM accounts WHERE club_id = c.id)
ON CONFLICT (club_id, code) DO NOTHING;

UPDATE stripe_balance_transactions t
   SET status = 'unbooked', review_reason = NULL, updated_at = now()
 WHERE t.reporting_category IN ('dispute', 'dispute_reversal')
   AND t.status = 'needs_review'
   AND t.review_reason IN ('refund_legacy', 'refund_pending_bry446')
   AND t.journal_entry_id IS NULL
   AND t.currency = 'nok'
   AND NOT EXISTS (SELECT 1 FROM stripe_payouts p
                    WHERE p.id = t.payout_id AND p.journal_entry_id IS NOT NULL);

-- Disputes still held as refund_pending_bry446 that the update above left
-- (their payout already cleared in the books, or not in NOK) must not be
-- reclassified to unbooked by a re-sync, which skips refund_legacy.
UPDATE stripe_balance_transactions
   SET review_reason = 'refund_legacy', updated_at = now()
 WHERE reporting_category IN ('dispute', 'dispute_reversal')
   AND status = 'needs_review'
   AND review_reason = 'refund_pending_bry446'
   AND journal_entry_id IS NULL;

-- payment_disputes holds only ids, an amount and Stripe's status and reason
-- codes, so the read-only dev role reads it raw.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN
        EXECUTE 'GRANT SELECT ON payment_disputes TO brygge_dev_ro';
    END IF;
END
$$;
