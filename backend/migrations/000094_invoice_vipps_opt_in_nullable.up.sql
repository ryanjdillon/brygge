-- Make the per-invoice Vipps-link opt-in a nullable tri-state (BRY-283).
--
-- Migration 000088 added vipps_payment_link_opt_in as BOOLEAN NOT NULL DEFAULT
-- false for a create-modal checkbox that was later removed (abed5e6); the column
-- has been dead ever since, so every row holds the meaningless default. Repurpose
-- it as the per-invoice override of the club's Vipps amount cap:
--   NULL  = Auto  (offer Vipps iff total_amount <= clubs.vipps_max_amount)
--   TRUE  = On    (always offer, even above the cap)
--   FALSE = Off   (never offer, even below the cap)
-- Drop the default+NOT NULL and reset the dead values to NULL (Auto).
ALTER TABLE invoices ALTER COLUMN vipps_payment_link_opt_in DROP DEFAULT;
ALTER TABLE invoices ALTER COLUMN vipps_payment_link_opt_in DROP NOT NULL;
UPDATE invoices SET vipps_payment_link_opt_in = NULL WHERE vipps_payment_link_opt_in = false;
