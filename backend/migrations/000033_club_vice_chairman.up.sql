-- Nestleder (vice chairman) was missing from the board contact set.
-- Slots into the existing pattern alongside chairman/treasurer/etc.
ALTER TABLE clubs ADD COLUMN vice_chairman_email TEXT NOT NULL DEFAULT '';
