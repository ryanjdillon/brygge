CREATE TABLE communication_preferences (
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    club_id       UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    category      TEXT NOT NULL,
    email_enabled BOOLEAN NOT NULL DEFAULT true,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, club_id, category)
);
