ALTER TABLE clubs
  DROP COLUMN IF EXISTS feature_feedback,
  DROP COLUMN IF EXISTS feedback_linear_api_key,
  DROP COLUMN IF EXISTS feedback_linear_team_id,
  DROP COLUMN IF EXISTS feedback_linear_triage_id;
