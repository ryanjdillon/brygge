ALTER TABLE order_lines DROP CONSTRAINT IF EXISTS order_lines_variant_id_fkey;
ALTER TABLE order_lines ADD CONSTRAINT order_lines_variant_id_fkey
    FOREIGN KEY (variant_id) REFERENCES product_variants(id);

ALTER TABLE order_lines DROP CONSTRAINT IF EXISTS order_lines_price_item_id_fkey;
ALTER TABLE order_lines ADD CONSTRAINT order_lines_price_item_id_fkey
    FOREIGN KEY (price_item_id) REFERENCES price_items(id);

ALTER TABLE order_lines DROP CONSTRAINT IF EXISTS order_lines_product_id_fkey;
ALTER TABLE order_lines ADD CONSTRAINT order_lines_product_id_fkey
    FOREIGN KEY (product_id) REFERENCES products(id);

ALTER TABLE product_variants DROP COLUMN IF EXISTS image_url;
