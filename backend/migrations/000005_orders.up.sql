CREATE TYPE order_status AS ENUM ('pending', 'paid', 'failed', 'refunded');

CREATE TABLE orders (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id         UUID NOT NULL REFERENCES clubs(id),
    user_id         UUID REFERENCES users(id),
    guest_email     TEXT NOT NULL DEFAULT '',
    guest_name      TEXT NOT NULL DEFAULT '',
    status          order_status NOT NULL DEFAULT 'pending',
    total_amount    NUMERIC NOT NULL,
    currency        TEXT NOT NULL DEFAULT 'NOK',
    vipps_reference TEXT NOT NULL DEFAULT '',
    paid_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_orders_club ON orders (club_id);
CREATE INDEX idx_orders_user ON orders (user_id);
CREATE INDEX idx_orders_vipps ON orders (vipps_reference) WHERE vipps_reference != '';

CREATE TABLE order_lines (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id    UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    product_id  UUID REFERENCES products(id),
    price_item_id UUID REFERENCES price_items(id),
    name        TEXT NOT NULL,
    quantity    INT NOT NULL DEFAULT 1,
    unit_price  NUMERIC NOT NULL,
    total_price NUMERIC NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_order_lines_order ON order_lines (order_id);
