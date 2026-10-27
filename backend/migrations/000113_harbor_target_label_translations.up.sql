-- Per-language names for harbor QR payment targets (BRY-437). A map from UI
-- locale code to text; the printed card shows the selected languages under the
-- club-language label, and the public /qr/{token} page shows the visitor's
-- locale. Keys and lengths are validated in the handler against the same
-- locale list as clubs.default_language.
ALTER TABLE harbor_payment_targets
    ADD COLUMN IF NOT EXISTS label_translations JSONB NOT NULL DEFAULT '{}'::jsonb;
