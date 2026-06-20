DROP TABLE IF EXISTS broadcast_deliveries;

ALTER TABLE broadcasts DROP COLUMN IF EXISTS status;
ALTER TABLE broadcasts DROP COLUMN IF EXISTS body_html;
ALTER TABLE broadcasts DROP COLUMN IF EXISTS source_address;
