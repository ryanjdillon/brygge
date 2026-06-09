ALTER TABLE clubs
  DROP COLUMN IF EXISTS feature_bookings,
  DROP COLUMN IF EXISTS feature_projects,
  DROP COLUMN IF EXISTS feature_calendar,
  DROP COLUMN IF EXISTS feature_commerce,
  DROP COLUMN IF EXISTS feature_communications,
  DROP COLUMN IF EXISTS feature_accounting;
