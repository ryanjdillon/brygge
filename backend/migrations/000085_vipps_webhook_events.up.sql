CREATE TABLE vipps_webhook_events (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id         UUID        NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    event_id        TEXT        NOT NULL,
    event_name      TEXT        NOT NULL,
    reference       TEXT        NOT NULL,
    payload         JSONB       NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX vipps_webhook_events_dedup  ON vipps_webhook_events(club_id, event_id);
CREATE        INDEX vipps_webhook_events_ref    ON vipps_webhook_events(club_id, reference);
CREATE        INDEX vipps_webhook_events_name   ON vipps_webhook_events(club_id, event_name);
