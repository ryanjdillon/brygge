-- The board's "can't rejoin" mark (owner decision 2026-11-06, BRY-476). A
-- former member may rejoin through the self-service join form unless the board
-- has set this; the join form then mails the address a pointer to the board
-- instead of a confirmation link, and nothing is created.
ALTER TABLE users ADD COLUMN IF NOT EXISTS rejoin_blocked BOOLEAN NOT NULL DEFAULT false;
