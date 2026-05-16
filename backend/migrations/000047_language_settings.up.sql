-- Per-club default UI/content language and per-member UI language
-- preference. Codes are the 8 supported locales (nb/nn Norwegian
-- Bokmål/Nynorsk + en/de/fr/it/nl/pl). Default 'nb'; a NULL
-- preferred_language means "follow the club default". DIL-329/336/337.

ALTER TABLE clubs
  ADD COLUMN default_language TEXT NOT NULL DEFAULT 'nb'
    CHECK (default_language IN ('nb','nn','en','de','fr','it','nl','pl'));

ALTER TABLE users
  ADD COLUMN preferred_language TEXT
    CHECK (preferred_language IN ('nb','nn','en','de','fr','it','nl','pl'));
