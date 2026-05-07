ALTER TABLE clubs DROP COLUMN IF EXISTS site_logo_mime;
ALTER TABLE clubs DROP COLUMN IF EXISTS site_logo_data;

ALTER TABLE clubs RENAME COLUMN faktura_logo_mime TO logo_mime;
ALTER TABLE clubs RENAME COLUMN faktura_logo_data TO logo_data;
