-- Auto-assignment of unambiguous KID-less bank deposits (BRY-366).
--
-- clubs.bank_auto_assign: opt-in, separate from vipps_auto_assign so a club
-- that books Vipps charges automatically has its bank deposits booked only
-- once a treasurer has checked the projection and turned this on. While it
-- is off, the bank KID cascade keeps its older name+amount step.
ALTER TABLE clubs ADD COLUMN IF NOT EXISTS bank_auto_assign BOOLEAN NOT NULL DEFAULT false;
