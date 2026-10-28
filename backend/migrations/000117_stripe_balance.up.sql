-- The Stripe balance, mirrored for the books.
--
-- stripe_payouts: one row per Stripe payout to the club's bank account.
-- members_listed_at is set once Stripe listed the transactions an automatic
-- payout paid out, so membership is known. bank_row_id and journal_entry_id
-- record the bank deposit it was matched to and the clearing entry, once
-- those exist.
--
-- stripe_balance_transactions: one row per Stripe balance transaction
-- (charge, fee, refund, dispute, payout, ...), classified at ingest. status
-- is the booking state: unbooked rows are ready to book, needs_review rows
-- carry a review_reason and wait for a person, booked rows have their
-- journal entry, dismissed rows were set aside by a person. payout_id is the
-- payout that paid the row out. stripe_type is Stripe's own type, finer than
-- reporting_category; fee_details is Stripe's fee breakdown (type, amount,
-- currency, description). Only ids, amounts, codes and Stripe's own fee
-- descriptions are copied; the charge itself (with the card holder's billing
-- details) is never stored.
--
-- stripe_sync_state: per club, how far the balance list has been read in
-- full. It moves only after a complete, error-free sync, so a run that
-- failed part-way is read again from the same point.
--
-- clubs.stripe_payout_descriptor: the regular expression that recognises a
-- Stripe payout in the bank statement text.

CREATE TABLE IF NOT EXISTS stripe_payouts (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id              UUID        NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    payout_id            TEXT        NOT NULL,
    amount_ore           BIGINT      NOT NULL,
    currency             TEXT        NOT NULL,
    arrival_date         DATE        NOT NULL,
    status               TEXT        NOT NULL,
    automatic            BOOLEAN     NOT NULL,
    statement_descriptor TEXT        NOT NULL DEFAULT '',
    members_listed_at    TIMESTAMPTZ,
    bank_row_id          UUID        REFERENCES bank_import_rows(id) ON DELETE SET NULL,
    journal_entry_id     UUID        REFERENCES journal_entries(id),
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'stripe_payouts_status_check') THEN
        ALTER TABLE stripe_payouts ADD CONSTRAINT stripe_payouts_status_check
            CHECK (status IN ('pending', 'in_transit', 'paid', 'failed', 'canceled'));
    END IF;
END
$$;

CREATE UNIQUE INDEX IF NOT EXISTS idx_stripe_payouts_club_payout
    ON stripe_payouts (club_id, payout_id);

CREATE TABLE IF NOT EXISTS stripe_balance_transactions (
    id                     UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id                UUID        NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    balance_transaction_id TEXT        NOT NULL,
    reporting_category     TEXT        NOT NULL,
    stripe_type            TEXT        NOT NULL DEFAULT '',
    amount_ore             BIGINT      NOT NULL,
    fee_ore                BIGINT      NOT NULL DEFAULT 0,
    net_ore                BIGINT      NOT NULL,
    fee_details            JSONB       NOT NULL DEFAULT '[]'::jsonb,
    currency               TEXT        NOT NULL,
    created_at             TIMESTAMPTZ NOT NULL,
    available_on           DATE,
    source_id              TEXT        NOT NULL DEFAULT '',
    charge_id              TEXT        NOT NULL DEFAULT '',
    payment_intent_id      TEXT        NOT NULL DEFAULT '',
    attempt_id             UUID        REFERENCES payment_attempts(id),
    order_id               UUID        REFERENCES orders(id),
    payout_id              UUID        REFERENCES stripe_payouts(id),
    journal_entry_id       UUID        REFERENCES journal_entries(id),
    invoice_id             UUID        REFERENCES invoices(id),
    status                 TEXT        NOT NULL DEFAULT 'unbooked',
    review_reason          TEXT,
    dismissed_at           TIMESTAMPTZ,
    dismissed_by           UUID        REFERENCES users(id) ON DELETE SET NULL,
    dismissed_reason       TEXT,
    inserted_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'stripe_balance_transactions_category_check') THEN
        ALTER TABLE stripe_balance_transactions ADD CONSTRAINT stripe_balance_transactions_category_check
            CHECK (reporting_category IN ('charge', 'refund', 'dispute', 'dispute_reversal', 'fee',
                                          'payout', 'payout_reversal', 'other_adjustment'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'stripe_balance_transactions_status_check') THEN
        ALTER TABLE stripe_balance_transactions ADD CONSTRAINT stripe_balance_transactions_status_check
            CHECK (status IN ('unbooked', 'booked', 'needs_review', 'dismissed'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'stripe_balance_transactions_review_reason') THEN
        ALTER TABLE stripe_balance_transactions ADD CONSTRAINT stripe_balance_transactions_review_reason
            CHECK (status <> 'needs_review' OR review_reason IS NOT NULL);
    END IF;
END
$$;

CREATE UNIQUE INDEX IF NOT EXISTS idx_stripe_bt_club_txn
    ON stripe_balance_transactions (club_id, balance_transaction_id);
CREATE INDEX IF NOT EXISTS idx_stripe_bt_club_created
    ON stripe_balance_transactions (club_id, created_at);
CREATE INDEX IF NOT EXISTS idx_stripe_bt_payout
    ON stripe_balance_transactions (payout_id);
CREATE INDEX IF NOT EXISTS idx_stripe_bt_attempt
    ON stripe_balance_transactions (attempt_id);
CREATE INDEX IF NOT EXISTS idx_stripe_bt_club_status
    ON stripe_balance_transactions (club_id, status);

CREATE TABLE IF NOT EXISTS stripe_sync_state (
    club_id        UUID        PRIMARY KEY REFERENCES clubs(id) ON DELETE CASCADE,
    listed_through TIMESTAMPTZ NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE clubs ADD COLUMN IF NOT EXISTS stripe_payout_descriptor TEXT NOT NULL DEFAULT '(?i)stripe';

-- stripe_payouts holds only ids, amounts, codes and the payout's statement
-- descriptor, which is the club's own text, and stripe_sync_state only a
-- timestamp, so the read-only dev role reads both raw. stripe_balance_transactions has the staff-typed dismissed_reason
-- and is reached only through its dev_masked view.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN
        EXECUTE 'GRANT SELECT ON stripe_payouts TO brygge_dev_ro';
        EXECUTE 'GRANT SELECT ON stripe_sync_state TO brygge_dev_ro';
    END IF;
END
$$;
