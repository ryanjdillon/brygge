-- A hash cannot be turned back into the token the API would look up, so the
-- outstanding links are dropped: they last 15 minutes and are reissued on
-- request.
DELETE FROM magic_links;

ALTER TABLE magic_links DROP CONSTRAINT IF EXISTS magic_links_token_hash_sha256_hex;
ALTER TABLE magic_links DROP CONSTRAINT IF EXISTS magic_links_pkey;
ALTER TABLE magic_links DROP COLUMN IF EXISTS token_hash;
ALTER TABLE magic_links ADD COLUMN IF NOT EXISTS token TEXT NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conrelid = 'magic_links'::regclass AND contype = 'p') THEN
        ALTER TABLE magic_links ADD CONSTRAINT magic_links_pkey PRIMARY KEY (token);
    END IF;
END
$$;
