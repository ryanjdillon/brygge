-- Per-invoice Vipps ePayment link (BRY-8). When a faktura is issued and the
-- instance has Vipps configured, an ePayment is created so the member can pay
-- with one tap instead of typing kontonr + KID. The reference is stored
-- explicitly (rather than assumed equal to the invoice id) because a re-issue
-- must mint a fresh payment with a new unique reference when the prior link
-- has expired. expires_at drives the re-issue decision and is surfaced in the
-- email. All nullable: invoices issued without Vipps keep the KID-only flow.
--
-- vipps_paid_at is a soft "Vipps payment received" marker set by the ePayment
-- CAPTURED webhook. It is deliberately NOT payment_id: during the CSV overlap
-- the settlement import remains the single source of the GL posting and the
-- authoritative paid status (it sets payment_id), and it only matches invoices
-- with payment_id IS NULL. Setting payment_id from the webhook would orphan
-- that reconciliation. This timestamp just drives a realtime "mottatt, venter
-- oppgjør" indicator until settlement clears it. See BRY-256 for the eventual
-- API-primary GL ownership (gated on BRY-4 Report API settlements).
ALTER TABLE invoices
  ADD COLUMN vipps_payment_reference TEXT,
  ADD COLUMN vipps_payment_url       TEXT,
  ADD COLUMN vipps_payment_expires_at TIMESTAMPTZ,
  ADD COLUMN vipps_paid_at            TIMESTAMPTZ;

-- Resolve an incoming ePayment webhook (reference) back to its invoice.
CREATE INDEX idx_invoices_vipps_payment_reference
  ON invoices (vipps_payment_reference)
  WHERE vipps_payment_reference IS NOT NULL;
