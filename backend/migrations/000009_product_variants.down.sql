ALTER TABLE order_lines DROP COLUMN IF EXISTS variant_id;
DROP TABLE IF EXISTS product_variants;
