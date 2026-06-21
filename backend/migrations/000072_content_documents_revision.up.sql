-- Reconcile content_documents on deployments where the `revision` and
-- `published_at` columns were added to the already-applied 000066
-- migration after the fact (commit 2cf5614). golang-migrate never re-ran
-- 000066, so the live table is missing both columns and the handlers
-- 500 with "column cd.revision does not exist".
--
-- Idempotent: a no-op on fresh databases where 000066 already created
-- the columns.

ALTER TABLE content_documents ADD COLUMN IF NOT EXISTS revision     INTEGER     NOT NULL DEFAULT 0;
ALTER TABLE content_documents ADD COLUMN IF NOT EXISTS published_at TIMESTAMPTZ;
