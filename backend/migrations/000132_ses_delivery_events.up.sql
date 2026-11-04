-- BRY-588: a row per outgoing email and the delivery events Amazon SES
-- reports back for it (SES → SNS → SQS → the API's event worker).
--
-- email_sends.id is the send_id message tag, so an event names its send
-- directly. Both tables hold recipient addresses, which are personal data:
-- user_id cascades, the member erasure paths delete a member's rows, and the
-- retention sweep drops rows older than 13 months (email_sends.created_at,
-- mail_delivery_events.received_at). The dev_masked views NULL the addresses.
--
-- The constraints are declared inside CREATE TABLE IF NOT EXISTS, so a
-- re-run that finds the table skips them along with it.

CREATE TABLE IF NOT EXISTS email_sends (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id          UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    user_id          UUID REFERENCES users(id) ON DELETE CASCADE,
    kind             TEXT NOT NULL,
    recipient        TEXT NOT NULL,
    recipient_domain TEXT NOT NULL,
    related_type     TEXT,
    related_id       UUID,
    provider         TEXT NOT NULL DEFAULT 'ses',
    status           TEXT NOT NULL,
    ses_message_id   TEXT,
    error            TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_event_at    TIMESTAMPTZ,
    CONSTRAINT email_sends_provider_check CHECK (provider IN ('ses', 'stalwart')),
    -- sending..unknown are set by the sender before SES has said anything;
    -- the rest are mapped from delivery events and only ever move up in rank.
    CONSTRAINT email_sends_status_check CHECK (status IN (
        'sending', 'submitted', 'failed', 'unknown',
        'queued', 'accepted', 'deferred', 'rejected_temporary',
        'rejected', 'suppressed', 'complaint'))
);

-- An event carries the SES message id; a send learns it once SES accepts the
-- message, so it is unique only once set.
CREATE UNIQUE INDEX IF NOT EXISTS idx_email_sends_ses_message_id
    ON email_sends (ses_message_id) WHERE ses_message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_email_sends_club_created
    ON email_sends (club_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_email_sends_related
    ON email_sends (related_type, related_id);
CREATE INDEX IF NOT EXISTS idx_email_sends_user
    ON email_sends (user_id);

CREATE TABLE IF NOT EXISTS mail_delivery_events (
    id               BIGSERIAL PRIMARY KEY,
    -- NULL when the event has no send_id tag or names a send that is gone.
    send_id          UUID REFERENCES email_sends(id) ON DELETE CASCADE,
    ses_message_id   TEXT NOT NULL,
    event_type       TEXT NOT NULL,
    event_at         TIMESTAMPTZ NOT NULL,
    recipient        TEXT,
    recipient_domain TEXT,
    bounce_type      TEXT,
    bounce_subtype   TEXT,
    delay_type       TEXT,
    smtp_status      TEXT,
    diagnostic       TEXT,
    remote_mta       TEXT,
    raw              JSONB NOT NULL,
    received_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT mail_delivery_events_event_type_check CHECK (event_type IN (
        'send', 'delivery', 'delivery_delay', 'bounce', 'complaint',
        'reject', 'rendering_failure')),
    -- SQS delivers at least once. A redelivered event hits this and is
    -- dropped; NULLS NOT DISTINCT so an event without a recipient (send,
    -- reject) is deduplicated too.
    CONSTRAINT mail_delivery_events_dedup
        UNIQUE NULLS NOT DISTINCT (ses_message_id, event_type, event_at, recipient)
);

CREATE INDEX IF NOT EXISTS idx_mail_delivery_events_send
    ON mail_delivery_events (send_id);
CREATE INDEX IF NOT EXISTS idx_mail_delivery_events_received
    ON mail_delivery_events (received_at);
