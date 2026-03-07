CREATE TABLE product_variants (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id  UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    size        TEXT NOT NULL DEFAULT '',
    color       TEXT NOT NULL DEFAULT '',
    stock       INT NOT NULL DEFAULT 0,
    price_override NUMERIC,
    sort_order  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_product_variants_product ON product_variants(product_id);
CREATE UNIQUE INDEX idx_product_variants_unique ON product_variants(product_id, size, color);

-- Add variant_id to order_lines so we track which variant was ordered
ALTER TABLE order_lines ADD COLUMN variant_id UUID REFERENCES product_variants(id);
