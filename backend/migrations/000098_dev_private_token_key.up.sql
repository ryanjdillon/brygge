-- Secret key for the keyed name tokens in the dev_masked views (BRY-343).
--
-- The views expose HMAC(name words, key) so the read-only brygge_dev_ro role
-- can tell that a bank payer and a member share a name without seeing it.
-- That only holds while the role cannot read the key, so it lives in its own
-- schema with no grants at all: the role lacks USAGE on the schema and
-- SELECT on the table (double denial), and default privileges on public can
-- never reach it. The views read it with their owner's (brygge) rights.
--
-- Numbered, not in the repeatable dev_masked.sql: that file drops and
-- recreates its schema on every deploy, which would rotate the key and
-- change every token. The key is generated server-side, so its value never
-- appears in SQL text, logs or git. Rotate on demand with
-- `just rotate-name-token-key`.

CREATE SCHEMA IF NOT EXISTS dev_private;
REVOKE ALL ON SCHEMA dev_private FROM PUBLIC;

CREATE TABLE IF NOT EXISTS dev_private.token_key (
    id         SMALLINT    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    key        BYTEA       NOT NULL,
    rotated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
REVOKE ALL ON dev_private.token_key FROM PUBLIC;

INSERT INTO dev_private.token_key (key)
VALUES (public.gen_random_bytes(32))
ON CONFLICT (id) DO NOTHING;
