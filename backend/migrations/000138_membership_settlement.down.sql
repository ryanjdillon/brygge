-- The dev_masked.clubs view reads the dropped column; the repeatable
-- dev_masked.sql rebuilds it from the current tables on the next migrate run.
DROP VIEW IF EXISTS dev_masked.clubs;

ALTER TABLE clubs DROP COLUMN IF EXISTS board_notification_email;

DROP INDEX IF EXISTS idx_deliveries_membership_notice_ref;

-- Membership rows cannot satisfy the narrower check, so they go first (any
-- queued membership email is lost on a down-migration).
DELETE FROM deliveries WHERE kind LIKE 'membership_%';

ALTER TABLE deliveries DROP CONSTRAINT IF EXISTS deliveries_parent_by_kind;
ALTER TABLE deliveries ADD CONSTRAINT deliveries_parent_by_kind CHECK (
    (kind = 'broadcast' AND broadcast_id IS NOT NULL AND invoice_id IS NULL)
    OR (kind LIKE 'invoice_%' AND invoice_id IS NOT NULL AND broadcast_id IS NULL)
);
