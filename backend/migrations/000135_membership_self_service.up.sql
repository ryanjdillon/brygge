-- Self-service membership: the schema for joining online and cancelling at
-- year end. Columns and tables only; the join, settlement and cancel paths
-- that read and write them come with their own changes.

-- Membership lifecycle on the member. member_since is the date membership was
-- granted (settlement of the join payment). A cancellation records when it was
-- asked for and the date it takes effect (31 Dec of that year); the member
-- stays active until then. membership_cancel_reason is the member's own free
-- text, so every erasure path clears it with the rest of the identity.
ALTER TABLE users ADD COLUMN IF NOT EXISTS member_since DATE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS membership_cancelled_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS membership_ends_on DATE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS membership_cancel_reason TEXT;

-- A join request waiting for its email confirmation. No users row exists until
-- the link is clicked, so an unconfirmed address never becomes an account. Only
-- the SHA-256 of the link token is stored; consumed_at makes the link
-- single-use. consent_versions maps each consent type to the legal-document
-- version shown at submit time. Rows past expires_at are purged.
CREATE TABLE IF NOT EXISTS membership_applications (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id          UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    first_name       TEXT NOT NULL,
    last_name        TEXT NOT NULL,
    email            TEXT NOT NULL,
    phone            TEXT,
    address_line     TEXT NOT NULL DEFAULT '',
    postal_code      TEXT NOT NULL DEFAULT '',
    city             TEXT NOT NULL DEFAULT '',
    is_local         BOOLEAN NOT NULL DEFAULT false,
    newsletter       BOOLEAN NOT NULL DEFAULT false,
    token_hash       TEXT NOT NULL UNIQUE,
    consent_versions JSONB NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ NOT NULL,
    consumed_at      TIMESTAMPTZ
);

-- The purge sweep's scan.
CREATE INDEX IF NOT EXISTS idx_membership_applications_expires
    ON membership_applications (expires_at);
-- Per-address lookups: the join rate limit and member erasure both match on
-- the address within a club, case-insensitively.
CREATE INDEX IF NOT EXISTS idx_membership_applications_club_email
    ON membership_applications (club_id, lower(email));

-- The price item a join is charged from. Unset means joining is closed for the
-- club; deleting the item closes it rather than failing the delete.
ALTER TABLE clubs ADD COLUMN IF NOT EXISTS join_price_item_id UUID
    REFERENCES price_items(id) ON DELETE SET NULL;

-- What a checkout order or a faktura is for, when settling it has a side
-- effect beyond clearing the receivable. NULL is the ordinary case. A pending
-- join order carries 'membership_join', and so does the KID faktura issued
-- when the applicant chooses bank payment; settling either grants membership.
ALTER TABLE orders ADD COLUMN IF NOT EXISTS purpose TEXT;
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS purpose TEXT;

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'orders_purpose_check' AND conrelid = 'orders'::regclass) THEN
    ALTER TABLE orders ADD CONSTRAINT orders_purpose_check
        CHECK (purpose IS NULL OR purpose IN ('membership_join'));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'invoices_purpose_check' AND conrelid = 'invoices'::regclass) THEN
    ALTER TABLE invoices ADD CONSTRAINT invoices_purpose_check
        CHECK (purpose IS NULL OR purpose IN ('membership_join'));
  END IF;
END $$;
