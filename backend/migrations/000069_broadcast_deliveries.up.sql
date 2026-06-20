-- Broadcast bulk-send delivery queue (BRY-162).
-- Extends broadcasts with the originating shared mailbox, an HTML body,
-- and a lifecycle status; adds a per-recipient delivery table that the
-- background worker drains. Guards every statement with IF [NOT] EXISTS
-- so a partially-applied migration can be retried (see AGENTS.md).

ALTER TABLE broadcasts ADD COLUMN IF NOT EXISTS source_address TEXT;
ALTER TABLE broadcasts ADD COLUMN IF NOT EXISTS body_html TEXT;
ALTER TABLE broadcasts ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'pending';

CREATE TABLE IF NOT EXISTS broadcast_deliveries (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    broadcast_id UUID NOT NULL REFERENCES broadcasts(id) ON DELETE CASCADE,
    club_id      UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    user_id      UUID REFERENCES users(id) ON DELETE SET NULL,
    email        TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    attempts     INT NOT NULL DEFAULT 0,
    error        TEXT NOT NULL DEFAULT '',
    sent_at      TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_broadcast_deliveries_broadcast ON broadcast_deliveries(broadcast_id);
CREATE INDEX IF NOT EXISTS idx_broadcast_deliveries_status ON broadcast_deliveries(status, created_at);
