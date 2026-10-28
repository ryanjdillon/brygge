-- Provider-neutral payment records.
--
-- payment_attempts: one row per payment created at a provider for an order.
-- An order is durable and a payer may start several attempts on it; each
-- keeps its own provider reference, so a late webhook for an earlier attempt
-- still finds its order. This is the source of truth for provider references;
-- orders.vipps_reference is still written for Vipps because the settlement
-- reconciliation matches on it.
--
-- payment_refunds: money returned for a captured attempt.
--
-- payment_webhook_events: raw provider events for providers other than
-- Vipps, which keeps vipps_webhook_events. One row per (club, provider,
-- event id), so a redelivered event is stored once.

CREATE TABLE IF NOT EXISTS payment_attempts (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id            UUID        NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    order_id           UUID        NOT NULL REFERENCES orders(id),
    provider           TEXT        NOT NULL,
    provider_reference TEXT,
    status             TEXT        NOT NULL DEFAULT 'created',
    amount             NUMERIC     NOT NULL,
    currency           TEXT        NOT NULL DEFAULT 'NOK',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'payment_attempts_status_check') THEN
        ALTER TABLE payment_attempts ADD CONSTRAINT payment_attempts_status_check
            CHECK (status IN ('created', 'authorized', 'captured', 'failed', 'cancelled'));
    END IF;
END
$$;

CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_attempts_provider_ref
    ON payment_attempts (provider, provider_reference) WHERE provider_reference IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_payment_attempts_order
    ON payment_attempts (order_id, created_at);

-- Every order that already carries a Vipps reference gets the attempt that
-- reference belongs to. Earlier attempts were overwritten on the order and are
-- not recoverable. A refunded order was captured first. The mint that stored
-- the reference also bumped orders.updated_at, so that is the closest record
-- of when the attempt was created; orders.created_at would date it to the
-- order's first attempt. Should two orders share a reference, the attempt
-- goes to the one updated last, the last to store it.
INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount, currency, created_at, updated_at)
SELECT DISTINCT ON (o.vipps_reference)
       o.club_id, o.id, 'vipps', o.vipps_reference,
       CASE o.status
           WHEN 'paid'::order_status     THEN 'captured'
           WHEN 'refunded'::order_status THEN 'captured'
           WHEN 'failed'::order_status   THEN 'failed'
           ELSE 'created'
       END,
       o.total_amount, o.currency, o.updated_at, o.updated_at
  FROM orders o
 WHERE o.vipps_reference <> ''
   AND NOT EXISTS (SELECT 1 FROM payment_attempts pa
                    WHERE pa.provider = 'vipps' AND pa.provider_reference = o.vipps_reference)
 ORDER BY o.vipps_reference, o.updated_at DESC;

CREATE TABLE IF NOT EXISTS payment_refunds (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id            UUID        NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    attempt_id         UUID        NOT NULL REFERENCES payment_attempts(id),
    provider           TEXT        NOT NULL,
    amount             NUMERIC     NOT NULL,
    currency           TEXT        NOT NULL DEFAULT 'NOK',
    provider_refund_id TEXT,
    status             TEXT        NOT NULL DEFAULT 'requested',
    reason             TEXT        NOT NULL DEFAULT '',
    created_by         UUID        REFERENCES users(id) ON DELETE SET NULL,
    journal_entry_id   UUID        REFERENCES journal_entries(id),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'payment_refunds_status_check') THEN
        ALTER TABLE payment_refunds ADD CONSTRAINT payment_refunds_status_check
            CHECK (status IN ('requested', 'succeeded', 'failed'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'payment_refunds_amount_positive') THEN
        ALTER TABLE payment_refunds ADD CONSTRAINT payment_refunds_amount_positive
            CHECK (amount > 0);
    END IF;
END
$$;

CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_refunds_provider_refund
    ON payment_refunds (provider, provider_refund_id) WHERE provider_refund_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_payment_refunds_attempt
    ON payment_refunds (attempt_id);

CREATE TABLE IF NOT EXISTS payment_webhook_events (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID        NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    provider    TEXT        NOT NULL,
    event_id    TEXT        NOT NULL,
    event_name  TEXT        NOT NULL,
    reference   TEXT        NOT NULL DEFAULT '',
    payload     JSONB       NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_webhook_events_dedup
    ON payment_webhook_events (club_id, provider, event_id);
CREATE INDEX IF NOT EXISTS idx_payment_webhook_events_ref
    ON payment_webhook_events (club_id, provider, reference);

-- payment_attempts holds no free text that can name a person, so the
-- read-only dev role reads it raw. payment_refunds (staff-typed reason) and
-- payment_webhook_events (payer data in the payload) are reached only
-- through their dev_masked views.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN
        EXECUTE 'GRANT SELECT ON payment_attempts TO brygge_dev_ro';
    END IF;
END
$$;
