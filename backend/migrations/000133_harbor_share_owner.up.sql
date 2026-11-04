-- Harbor share (moloandel) ownership as member fields (BRD-14). Is-owner is
-- always harbor_share_owner. harbor_share_paid_on is optional because buy-in
-- dates before 2026 are unknown; NULL means the date is not known, never that
-- the member is not an owner, and no placeholder date is ever stored.
-- DATE rather than TIMESTAMPTZ: handlers have no per-club timezone and nothing
-- that writes it has a time of day, so a DATE cannot shift by a day. No index:
-- small per-club table, and the other admin user filters are unindexed too.
ALTER TABLE users ADD COLUMN IF NOT EXISTS harbor_share_owner BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS harbor_share_paid_on DATE;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'users_harbor_share_paid_on_requires_owner'
           AND conrelid = 'users'::regclass
    ) THEN
        ALTER TABLE users ADD CONSTRAINT users_harbor_share_paid_on_requires_owner
            CHECK (harbor_share_paid_on IS NULL OR harbor_share_owner);
    END IF;
END $$;
