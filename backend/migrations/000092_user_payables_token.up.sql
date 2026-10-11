-- Durable per-member payables-link token (BRY-275).
--
-- A random, non-expiring token that identifies a member for the public
-- /betal/bruker/{token} payables page, where they see and pay their own open
-- invoices without logging in — for members who don't cope with magic-link
-- auth. It mirrors the capability model of the per-invoice orders.token
-- (000089): a bearer token, globally unique, nullable until first minted. The
-- user row already carries club_id, so the token alone scopes both member and
-- club on lookup.
ALTER TABLE users ADD COLUMN IF NOT EXISTS payables_token TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_payables_token
    ON users (payables_token) WHERE payables_token IS NOT NULL;
