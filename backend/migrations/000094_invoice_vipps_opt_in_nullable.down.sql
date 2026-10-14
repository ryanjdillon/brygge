-- Revert to BOOLEAN NOT NULL DEFAULT false: treat Auto/On as false, so no
-- invoice silently keeps a link once the tri-state is gone.
UPDATE invoices SET vipps_payment_link_opt_in = false WHERE vipps_payment_link_opt_in IS NULL;
ALTER TABLE invoices ALTER COLUMN vipps_payment_link_opt_in SET DEFAULT false;
ALTER TABLE invoices ALTER COLUMN vipps_payment_link_opt_in SET NOT NULL;
