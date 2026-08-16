-- Harbor QR payment targets (BRY-6): a printed poster's QR encodes the public
-- /qr/{token} checkout page. The payer lands there, picks nights/units, and
-- pays via the existing ePayment + CAPTURED webhook path (BRY-259/BRY-200) —
-- no Vipps QR API. Per-unit price and revenue category come from the linked
-- price_items row, so pricing has a single source of truth.
--
-- kind: guest_slip | motorhome | service — drives the harbor overview's payment
--   kind and (via the price_item category) the reconciliation account.
-- fixed_units: true for a flat-price service (no nights selector; quantity = 1).
CREATE TABLE harbor_payment_targets (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id       UUID NOT NULL REFERENCES clubs(id),
    token         TEXT NOT NULL,
    label         TEXT NOT NULL,
    location      TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL DEFAULT 'guest_slip',
    price_item_id UUID NOT NULL REFERENCES price_items(id),
    unit_label    TEXT NOT NULL DEFAULT 'natt',
    min_units     INT NOT NULL DEFAULT 1,
    max_units     INT NOT NULL DEFAULT 14,
    fixed_units   BOOLEAN NOT NULL DEFAULT false,
    archived_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_harbor_targets_token ON harbor_payment_targets (token);
CREATE INDEX idx_harbor_targets_club ON harbor_payment_targets (club_id) WHERE archived_at IS NULL;
