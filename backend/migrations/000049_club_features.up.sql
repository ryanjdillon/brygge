-- Per-club feature flags, replacing the env-var-only configuration
-- that the FEATURES_* variables provided at deploy time. The clubs
-- row is now the source of truth; admins can flip modules on/off
-- from Site settings without redeploying. The env values are kept
-- as fallback defaults (handled in Go) for backwards compatibility
-- in case the clubs row hasn't been backfilled yet.
--
-- Defaults are TRUE for parity with the historical "everything on
-- unless explicitly disabled" behavior most deploys ran with.

ALTER TABLE clubs
  ADD COLUMN feature_bookings       BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN feature_projects       BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN feature_calendar       BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN feature_commerce       BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN feature_communications BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN feature_accounting     BOOLEAN NOT NULL DEFAULT TRUE;
