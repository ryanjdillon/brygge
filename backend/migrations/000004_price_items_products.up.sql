-- Structured pricing catalog
CREATE TABLE price_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id         UUID NOT NULL REFERENCES clubs(id),
    category        TEXT NOT NULL,          -- moloandel, slip_fee, seasonal_rental, guest, bobil, room_hire, service, other
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    amount          NUMERIC NOT NULL,
    currency        TEXT NOT NULL DEFAULT 'NOK',
    unit            TEXT NOT NULL DEFAULT 'once', -- once, year, season, day, night, hour
    installments_allowed BOOLEAN NOT NULL DEFAULT false,
    max_installments     INT NOT NULL DEFAULT 1,
    metadata        JSONB NOT NULL DEFAULT '{}', -- season dates, size thresholds, etc.
    sort_order      INT NOT NULL DEFAULT 0,
    is_active       BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_price_items_club ON price_items (club_id);
CREATE INDEX idx_price_items_category ON price_items (club_id, category);

-- Merchandise product catalog
CREATE TABLE products (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id         UUID NOT NULL REFERENCES clubs(id),
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    price           NUMERIC NOT NULL,
    currency        TEXT NOT NULL DEFAULT 'NOK',
    image_url       TEXT NOT NULL DEFAULT '',
    stock           INT NOT NULL DEFAULT 0,
    is_active       BOOLEAN NOT NULL DEFAULT true,
    sort_order      INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_products_club ON products (club_id);
