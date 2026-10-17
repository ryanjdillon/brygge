-- Journal entries that carry balances over from a previous accounting
-- system at the cutover date (BRY-365). The credit-note link uses one with a
-- 1500 line as evidence that an imported, never-booked faktura's
-- receivable exists in Brygge's GL (BRY-370).
ALTER TYPE transaction_source ADD VALUE IF NOT EXISTS 'opening_balance';
