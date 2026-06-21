-- Drop the per-club feature_communications toggle. The Communications
-- module (push notifications, notification preferences/config, and
-- broadcasts) is now always-on and no longer operator-toggleable; the
-- forum piece was momentarily disabled in code instead (see BRY-191).
-- Guarded with IF EXISTS so a partially-applied migration can be retried.

ALTER TABLE clubs DROP COLUMN IF EXISTS feature_communications;
