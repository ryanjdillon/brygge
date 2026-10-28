-- How a Stripe payment was made, for the harbor payment overview: the card
-- brand Stripe reports on the charge (visa, mastercard, ...) and, when the
-- card was paid through a wallet, the wallet type (apple_pay, google_pay,
-- link, ...). Raw Stripe values, lowercase. NULL for Vipps attempts and for
-- card attempts whose charge has not been read yet. Never the card number,
-- last digits or issuing country.
--
-- Nullable with no default, so adding them rewrites nothing and holds the
-- table lock only briefly.
ALTER TABLE payment_attempts ADD COLUMN IF NOT EXISTS card_brand TEXT;
ALTER TABLE payment_attempts ADD COLUMN IF NOT EXISTS card_wallet TEXT;
