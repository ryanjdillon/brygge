-- Add image_url to product variants for color-specific images
ALTER TABLE product_variants ADD COLUMN image_url TEXT NOT NULL DEFAULT '';

-- Make order_lines FKs SET NULL so product/variant deletion doesn't fail
ALTER TABLE order_lines DROP CONSTRAINT IF EXISTS order_lines_product_id_fkey;
ALTER TABLE order_lines ADD CONSTRAINT order_lines_product_id_fkey
    FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE SET NULL;

ALTER TABLE order_lines DROP CONSTRAINT IF EXISTS order_lines_price_item_id_fkey;
ALTER TABLE order_lines ADD CONSTRAINT order_lines_price_item_id_fkey
    FOREIGN KEY (price_item_id) REFERENCES price_items(id) ON DELETE SET NULL;

ALTER TABLE order_lines DROP CONSTRAINT IF EXISTS order_lines_variant_id_fkey;
ALTER TABLE order_lines ADD CONSTRAINT order_lines_variant_id_fkey
    FOREIGN KEY (variant_id) REFERENCES product_variants(id) ON DELETE SET NULL;
