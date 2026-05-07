-- Split the single club logo into two purpose-specific logos:
--   * faktura_logo — used on the faktura PDF; PNG or JPEG (the PDF
--     library cannot rasterize SVG).
--   * site_logo    — used in the navbar and elsewhere on the public
--     site; SVG-only so it scales crisply at any size.
--
-- The pre-existing `logo_data`/`logo_mime` was the faktura logo in
-- practice (bulk + single faktura generators both read it), so we
-- rename in place and add the site logo as new columns.

ALTER TABLE clubs RENAME COLUMN logo_data TO faktura_logo_data;
ALTER TABLE clubs RENAME COLUMN logo_mime TO faktura_logo_mime;

ALTER TABLE clubs ADD COLUMN site_logo_data BYTEA;
ALTER TABLE clubs ADD COLUMN site_logo_mime TEXT NOT NULL DEFAULT '';
