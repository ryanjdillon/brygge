CREATE TABLE push_subscriptions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    club_id     UUID NOT NULL REFERENCES clubs(id),
    endpoint    TEXT NOT NULL,
    p256dh      TEXT NOT NULL,
    auth        TEXT NOT NULL,
    user_agent  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX idx_push_sub_endpoint ON push_subscriptions(endpoint);

CREATE TABLE notification_preferences (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    club_id     UUID NOT NULL REFERENCES clubs(id),
    category    TEXT NOT NULL,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    PRIMARY KEY (user_id, club_id, category)
);

CREATE TABLE notification_config (
    club_id     UUID NOT NULL REFERENCES clubs(id),
    category    TEXT NOT NULL,
    required    BOOLEAN NOT NULL DEFAULT false,
    lead_days   INT,
    PRIMARY KEY (club_id, category)
);
