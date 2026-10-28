-- Provider-neutral names for the online-payment columns that were added when
-- Vipps was the only provider. The old columns stay and are dual-written by
-- the application until a later migration drops them, so this file only adds
-- and backfills.
--
-- - invoices.online_paid_at / online_paid_provider: the soft "paid online,
--   awaiting settlement" marker, for any provider. Set together or not at all.
-- - invoices.online_payment_opt_in: the per-faktura tri-state override
--   (NULL Auto, TRUE On, FALSE Off) of the club's cap, for every provider.
-- - clubs.online_max_amount: the club's cap on online payment.
-- - clubs.payment_fee_account: where provider fees are booked, for every
--   provider.
-- - clubs.stripe_clearing_account: the receivable that holds a card payment
--   until the provider pays it out (1580, seeded into existing charts below).

ALTER TABLE invoices ADD COLUMN IF NOT EXISTS online_paid_at        TIMESTAMPTZ;
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS online_paid_provider  TEXT;
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS online_payment_opt_in BOOLEAN;

ALTER TABLE clubs ADD COLUMN IF NOT EXISTS online_max_amount       NUMERIC(12, 2);
ALTER TABLE clubs ADD COLUMN IF NOT EXISTS payment_fee_account     TEXT NOT NULL DEFAULT '7700';
ALTER TABLE clubs ADD COLUMN IF NOT EXISTS stripe_clearing_account TEXT NOT NULL DEFAULT '1580';

UPDATE invoices
   SET online_paid_at = vipps_paid_at,
       online_paid_provider = 'vipps'
 WHERE vipps_paid_at IS NOT NULL
   AND online_paid_at IS NULL;

UPDATE invoices
   SET online_payment_opt_in = vipps_payment_link_opt_in
 WHERE vipps_payment_link_opt_in IS NOT NULL
   AND online_payment_opt_in IS NULL;

UPDATE clubs
   SET online_max_amount = vipps_max_amount
 WHERE vipps_max_amount IS NOT NULL
   AND online_max_amount IS NULL;

UPDATE clubs
   SET payment_fee_account = vipps_fee_account
 WHERE payment_fee_account IS DISTINCT FROM vipps_fee_account;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'invoices_online_paid_provider_set') THEN
        ALTER TABLE invoices ADD CONSTRAINT invoices_online_paid_provider_set
            CHECK ((online_paid_at IS NULL) = (online_paid_provider IS NULL));
    END IF;
END
$$;

-- 1580 for clubs whose chart was seeded before it joined the default
-- kontoplan. A club that already has a 1580 keeps its own.
INSERT INTO accounts (club_id, code, name, account_type, parent_code, is_system, is_active, mva_eligible, description, sort_order)
SELECT c.id, '1580', 'Til gode betalingsformidler (Stripe)', 'asset'::account_type, '1000', true, true,
       'not_applicable'::mva_eligibility, 'Kortbetalinger hos Stripe som venter på utbetaling til bank', 15
FROM clubs c
WHERE EXISTS (SELECT 1 FROM accounts WHERE club_id = c.id)
ON CONFLICT (club_id, code) DO NOTHING;
