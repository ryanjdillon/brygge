-- Generalize the bulk-send delivery queue (BRY-11).
-- The broadcast_deliveries table was broadcast-specific; rename it to the
-- neutral `deliveries` and add a `kind` discriminator so invoice reminders
-- reuse the same durable, throttled, retried worker instead of a separate
-- in-memory queue. Broadcast rows keep broadcast_id; invoice rows carry
-- invoice_id plus a per-row JSONB payload with the send context the worker
-- needs (locale, club name, actor, ...). Guards with IF [NOT] EXISTS /
-- exception-swallow so a partially-applied migration can be retried
-- (see AGENTS.md).

ALTER TABLE IF EXISTS broadcast_deliveries RENAME TO deliveries;

ALTER TABLE deliveries ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'broadcast';
ALTER TABLE deliveries ALTER COLUMN broadcast_id DROP NOT NULL;
ALTER TABLE deliveries ADD COLUMN IF NOT EXISTS invoice_id UUID REFERENCES invoices(id) ON DELETE CASCADE;
ALTER TABLE deliveries ADD COLUMN IF NOT EXISTS payload JSONB;

-- Exactly one parent per kind: broadcasts point at a broadcasts row, invoice
-- kinds point at an invoices row. Prevents an orphaned or mis-typed delivery.
DO $$ BEGIN
    ALTER TABLE deliveries ADD CONSTRAINT deliveries_parent_by_kind CHECK (
        (kind = 'broadcast' AND broadcast_id IS NOT NULL AND invoice_id IS NULL)
        OR (kind LIKE 'invoice_%' AND invoice_id IS NOT NULL AND broadcast_id IS NULL)
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Reuse the existing indexes under neutral names; add a kind-scoped one so
-- the worker's per-kind claim (ORDER BY created_at) stays index-backed.
ALTER INDEX IF EXISTS idx_broadcast_deliveries_status RENAME TO idx_deliveries_status;
ALTER INDEX IF EXISTS idx_broadcast_deliveries_broadcast RENAME TO idx_deliveries_broadcast;
CREATE INDEX IF NOT EXISTS idx_deliveries_kind_status ON deliveries(kind, status, created_at);
