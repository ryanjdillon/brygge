-- Per-faktura opt-in for the "Betal med Vipps" link (BRY-8). Chosen by the
-- creator in the Create-faktura modal (default false — bank transfer stays the
-- norm) and honoured when the draft is sent: HandleSendInvoice mints the
-- ePayment only when this is true. Stored as a flag rather than eagerly
-- creating the payment at draft time so the link is minted against the final
-- amount at send.
ALTER TABLE invoices
  ADD COLUMN vipps_payment_link_opt_in BOOLEAN NOT NULL DEFAULT false;
