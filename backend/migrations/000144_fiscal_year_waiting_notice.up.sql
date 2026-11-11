-- A payment waiting for a missing, closed or locked fiscal year tells the
-- treasurer once per year and state (BRY-575). The notice goes through the
-- shared delivery queue as kind accounting_fiscal_year_waiting; it hangs off
-- no broadcast or faktura, so the parent-by-kind check gains an accounting_*
-- branch with neither parent set (as membership_* in 000138).
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'deliveries_parent_by_kind' AND conrelid = 'deliveries'::regclass) THEN
    ALTER TABLE deliveries DROP CONSTRAINT deliveries_parent_by_kind;
  END IF;
  ALTER TABLE deliveries ADD CONSTRAINT deliveries_parent_by_kind CHECK (
      (kind = 'broadcast' AND broadcast_id IS NOT NULL AND invoice_id IS NULL)
      OR (kind LIKE 'invoice_%' AND invoice_id IS NOT NULL AND broadcast_id IS NULL)
      OR (kind LIKE 'membership_%' AND broadcast_id IS NULL AND invoice_id IS NULL)
      OR (kind LIKE 'accounting_%' AND broadcast_id IS NULL AND invoice_id IS NULL)
  );
END $$;

-- One notice per condition: payload->>'ref' is
-- fiscal_year_waiting:<club>:<year>:missing, or for a closed or locked period
-- fiscal_year_waiting:<club>:<year>:<state>:<period>:<closed_at unix>, so the
-- hourly sweep finds the row and queues nothing more, and closing the year
-- again after a reopen tells the treasurer again.
CREATE UNIQUE INDEX IF NOT EXISTS idx_deliveries_fiscal_year_waiting_ref
    ON deliveries (kind, (payload->>'ref'))
    WHERE kind = 'accounting_fiscal_year_waiting';
