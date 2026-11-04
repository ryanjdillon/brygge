-- The dev_masked.journal_entries view lists reverses_entry_id; the repeatable
-- dev_masked.sql rebuilds it from the current table on the next migrate run.
DROP VIEW IF EXISTS dev_masked.journal_entries;
DROP INDEX IF EXISTS idx_journal_entries_reverses_entry_id;
ALTER TABLE journal_entries DROP COLUMN IF EXISTS reverses_entry_id;
