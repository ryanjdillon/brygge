DROP TABLE IF EXISTS order_sources;
DROP INDEX IF EXISTS idx_orders_vipps_reference;
DROP INDEX IF EXISTS idx_orders_token;
ALTER TABLE orders DROP COLUMN IF EXISTS provider;
ALTER TABLE orders DROP COLUMN IF EXISTS token;
