-- Editable content for the Guest harbour (/harbor) and Motorhome
-- parking (/motorhome) public pages. Each column maps to one card on
-- those pages; an empty string means "use the i18n fallback baked
-- into the frontend".
ALTER TABLE clubs ADD COLUMN harbor_approach           TEXT NOT NULL DEFAULT '';
ALTER TABLE clubs ADD COLUMN harbor_depth              TEXT NOT NULL DEFAULT '';
ALTER TABLE clubs ADD COLUMN harbor_vhf                TEXT NOT NULL DEFAULT '';
ALTER TABLE clubs ADD COLUMN harbor_cta_title          TEXT NOT NULL DEFAULT '';
ALTER TABLE clubs ADD COLUMN harbor_cta_description    TEXT NOT NULL DEFAULT '';
ALTER TABLE clubs ADD COLUMN motorhome_power           TEXT NOT NULL DEFAULT '';
ALTER TABLE clubs ADD COLUMN motorhome_facilities      TEXT NOT NULL DEFAULT '';
ALTER TABLE clubs ADD COLUMN motorhome_checkin         TEXT NOT NULL DEFAULT '';
ALTER TABLE clubs ADD COLUMN motorhome_rules           TEXT NOT NULL DEFAULT '';
ALTER TABLE clubs ADD COLUMN motorhome_cta_title       TEXT NOT NULL DEFAULT '';
ALTER TABLE clubs ADD COLUMN motorhome_cta_description TEXT NOT NULL DEFAULT '';
