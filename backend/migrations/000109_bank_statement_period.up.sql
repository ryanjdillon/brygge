-- BRY-378: tell a provisional in-flight bank row from a settled one.
--
-- bank_imports.period_from / period_to record the date span of the uploaded
-- statement. "The export day" is period_to; deriving it from the rows that
-- survive (max(row_date)) shifts when an overwrite reimport deletes some of
-- an import's rows. Existing imports are backfilled from their current rows,
-- the best record available.
--
-- bank_import_rows.reseen_at is set when a later statement that runs past
-- the row's date carries the same bare "Overførsel" again: the row is then
-- a settled transfer with a terse text, not an in-flight one, and is never
-- superseded.

ALTER TABLE bank_imports ADD COLUMN IF NOT EXISTS period_from DATE;
ALTER TABLE bank_imports ADD COLUMN IF NOT EXISTS period_to DATE;
ALTER TABLE bank_import_rows ADD COLUMN IF NOT EXISTS reseen_at TIMESTAMPTZ;

UPDATE bank_imports bi
   SET period_from = r.from_date, period_to = r.to_date
  FROM (SELECT bank_import_id, min(row_date) AS from_date, max(row_date) AS to_date
          FROM bank_import_rows GROUP BY bank_import_id) r
 WHERE r.bank_import_id = bi.id AND bi.period_to IS NULL;
