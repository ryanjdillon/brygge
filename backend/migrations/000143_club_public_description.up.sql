-- The club's short public description (BRY-666): one line of plain text the
-- landing page shows under the welcome heading. Empty (the default) means the
-- page uses its translated default tagline, so no club gets a text it did not
-- write. The handler trims it and caps it at 200 characters.
ALTER TABLE clubs ADD COLUMN IF NOT EXISTS public_description TEXT NOT NULL DEFAULT '';
