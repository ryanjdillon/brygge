-- Short typed address for harbor QR payment targets (BRY-436). A printed card
-- carries /q/{short_code} under the QR as a fallback for a guest whose phone
-- will not scan; the server redirects it to the target's /qr/{token} page.
-- The QR itself keeps encoding the token URL, so codes already printed keep
-- working whatever happens to the short code.
--
-- Codes are stored lowercase, so the plain (club_id, short_code) index is
-- case-insensitive in effect. Existing rows get a generated code from the
-- same alphabet as the Go generator: lowercase letters and digits without
-- the look-alikes 0, o, 1, l and i.
ALTER TABLE harbor_payment_targets ADD COLUMN IF NOT EXISTS short_code TEXT;

DO $$
DECLARE
    alphabet CONSTANT TEXT := 'abcdefghjkmnpqrstuvwxyz23456789';
    target   RECORD;
    candidate TEXT;
BEGIN
    FOR target IN
        SELECT id, club_id FROM harbor_payment_targets WHERE short_code IS NULL
    LOOP
        LOOP
            candidate := '';
            FOR n IN 1..5 LOOP
                candidate := candidate
                    || substr(alphabet, 1 + floor(random() * length(alphabet))::int, 1);
            END LOOP;
            EXIT WHEN NOT EXISTS (
                SELECT 1 FROM harbor_payment_targets
                 WHERE club_id = target.club_id AND short_code = candidate
            );
        END LOOP;
        UPDATE harbor_payment_targets SET short_code = candidate WHERE id = target.id;
    END LOOP;
END
$$;

ALTER TABLE harbor_payment_targets ALTER COLUMN short_code SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_harbor_targets_club_short_code
    ON harbor_payment_targets (club_id, short_code);

-- Scoped to this table rather than the constraint name alone, so the guard
-- still adds the constraint when the migrations run in several schemas of
-- one database (the integration-test setup).
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'harbor_payment_targets_short_code_format'
           AND conrelid = 'harbor_payment_targets'::regclass
    ) THEN
        ALTER TABLE harbor_payment_targets ADD CONSTRAINT harbor_payment_targets_short_code_format
            CHECK (short_code ~ '^[a-z0-9-]{2,24}$');
    END IF;
END
$$;
