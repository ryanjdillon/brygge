-- Reverse 000075: collapse `deliveries` back to the broadcast-only
-- broadcast_deliveries table. Non-broadcast rows can't satisfy the old
-- NOT NULL broadcast_id, so drop them first (best-effort — any queued
-- invoice reminders are lost on a down-migration).

DELETE FROM deliveries WHERE kind <> 'broadcast';

ALTER TABLE deliveries DROP CONSTRAINT IF EXISTS deliveries_parent_by_kind;
DROP INDEX IF EXISTS idx_deliveries_kind_status;
ALTER INDEX IF EXISTS idx_deliveries_status RENAME TO idx_broadcast_deliveries_status;
ALTER INDEX IF EXISTS idx_deliveries_broadcast RENAME TO idx_broadcast_deliveries_broadcast;

ALTER TABLE deliveries DROP COLUMN IF EXISTS payload;
ALTER TABLE deliveries DROP COLUMN IF EXISTS invoice_id;
ALTER TABLE deliveries DROP COLUMN IF EXISTS kind;
ALTER TABLE deliveries ALTER COLUMN broadcast_id SET NOT NULL;

ALTER TABLE IF EXISTS deliveries RENAME TO broadcast_deliveries;
