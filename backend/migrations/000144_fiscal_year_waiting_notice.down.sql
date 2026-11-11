-- Accounting rows cannot satisfy the narrower check, so they go first (a
-- queued fiscal year notice is lost on a down-migration; the hourly sweep
-- queues it again once the kind is back).
DELETE FROM deliveries WHERE kind LIKE 'accounting_%';

DROP INDEX IF EXISTS idx_deliveries_fiscal_year_waiting_ref;

DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'deliveries_parent_by_kind' AND conrelid = 'deliveries'::regclass) THEN
    ALTER TABLE deliveries DROP CONSTRAINT deliveries_parent_by_kind;
  END IF;
  ALTER TABLE deliveries ADD CONSTRAINT deliveries_parent_by_kind CHECK (
      (kind = 'broadcast' AND broadcast_id IS NOT NULL AND invoice_id IS NULL)
      OR (kind LIKE 'invoice_%' AND invoice_id IS NOT NULL AND broadcast_id IS NULL)
      OR (kind LIKE 'membership_%' AND broadcast_id IS NULL AND invoice_id IS NULL)
  );
END $$;
