-- Store login tokens only as sha256 hex (BRY-652), as membership_applications
-- already does. The emailed link keeps the raw token; a database read alone
-- can no longer log anyone in.
ALTER TABLE magic_links ADD COLUMN IF NOT EXISTS token_hash TEXT;

-- Links still inside their 15 minutes keep working across the deploy. The
-- token is base64url ASCII, so its UTF-8 bytes are the bytes the API hashes.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_schema = current_schema()
                 AND table_name = 'magic_links' AND column_name = 'token') THEN
        EXECUTE $q$UPDATE magic_links
                   SET token_hash = encode(sha256(convert_to(token, 'UTF8')), 'hex')
                   WHERE token_hash IS NULL$q$;
    END IF;
END
$$;

ALTER TABLE magic_links ALTER COLUMN token_hash SET NOT NULL;

-- Dropping the raw column drops its primary key with it.
ALTER TABLE magic_links DROP COLUMN IF EXISTS token;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conrelid = 'magic_links'::regclass AND contype = 'p') THEN
        ALTER TABLE magic_links ADD CONSTRAINT magic_links_pkey PRIMARY KEY (token_hash);
    END IF;
END
$$;

-- Only a sha256 hex fits, so a raw token written by mistake is refused.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conrelid = 'magic_links'::regclass
                     AND conname = 'magic_links_token_hash_sha256_hex') THEN
        ALTER TABLE magic_links ADD CONSTRAINT magic_links_token_hash_sha256_hex
            CHECK (token_hash ~ '^[0-9a-f]{64}$');
    END IF;
END
$$;
