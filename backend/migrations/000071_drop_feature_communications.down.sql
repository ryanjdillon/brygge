-- Restore the feature_communications toggle, defaulting TRUE for parity
-- with the historical "on unless disabled" behavior.

ALTER TABLE clubs
  ADD COLUMN IF NOT EXISTS feature_communications BOOLEAN NOT NULL DEFAULT TRUE;
