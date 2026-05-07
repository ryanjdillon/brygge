-- Reverting requires deleting any user-less rows first. We won't do
-- that automatically — the down migration is best-effort.
ALTER TABLE invoices ALTER COLUMN user_id SET NOT NULL;
