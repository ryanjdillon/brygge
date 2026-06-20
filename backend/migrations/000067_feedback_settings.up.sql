ALTER TABLE clubs
  ADD COLUMN IF NOT EXISTS feature_feedback            BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS feedback_linear_api_key     TEXT    NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS feedback_linear_team_id     TEXT    NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS feedback_linear_triage_id   TEXT    NOT NULL DEFAULT '';
