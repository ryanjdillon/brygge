-- Unified checkout (BRY-259): the existing `orders` cart becomes the single
-- pay layer for merch, harbor QR, and faktura payment links.
--
-- - token: durable, unguessable capability that addresses the public
--   mint-on-click pay link (/api/v1/betal/<token>). The ePayment redirectUrl is
--   only valid ~10 minutes, so the email links here, not to Vipps directly.
-- - provider: 'vipps' today, room for 'stripe' later.
-- - order_sources: ties an order to the already-issued faktura(s) it settles.
--   The receivable is booked to 1500 at issue; paying only clears it (handled by
--   the settlement reconciler, not the webhook). Ad-hoc catalog add-ons remain
--   ordinary order_lines.
ALTER TABLE orders ADD COLUMN token    TEXT;
ALTER TABLE orders ADD COLUMN provider TEXT NOT NULL DEFAULT 'vipps';

CREATE UNIQUE INDEX idx_orders_token ON orders (token) WHERE token IS NOT NULL;
CREATE INDEX idx_orders_vipps_reference ON orders (vipps_reference) WHERE vipps_reference <> '';

CREATE TABLE order_sources (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id   UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    invoice_id UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE
);

CREATE INDEX idx_order_sources_order ON order_sources (order_id);
-- One durable checkout order per invoice: resends reuse it, an expired attempt
-- re-mints the reference on the same row instead of orphaning a second order.
CREATE UNIQUE INDEX idx_order_sources_invoice ON order_sources (invoice_id);
