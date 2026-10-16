-- Journal entries posted by ResplitInvoiceBilags (BRY-364): revenue
-- reclassifications that correct an invoice_sync entry's per-account split
-- without touching the original entry.
ALTER TYPE transaction_source ADD VALUE IF NOT EXISTS 'invoice_resplit';
