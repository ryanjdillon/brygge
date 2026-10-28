-- Journal entries booked from the Stripe balance (card charges, Stripe fees
-- and payout clearing). Kept alone in its file: Postgres cannot use an enum
-- value in the transaction that added it, and golang-migrate runs each file
-- as one transaction.
ALTER TYPE transaction_source ADD VALUE IF NOT EXISTS 'stripe';
