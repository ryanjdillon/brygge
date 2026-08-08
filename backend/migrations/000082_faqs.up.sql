-- Per-club public FAQ content (BRY-241/BRY-242). The FAQ feature (page,
-- accordion, route) ships in the product; the questions and answers are
-- club-specific data the board authors in its own language — mirroring the
-- harbor/motorhome public content in 000031. group_key is a small fixed set
-- so the public page can order and label sections via i18n chrome while the
-- text itself stays free-form and untranslated.
CREATE TABLE faqs (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id    UUID        NOT NULL REFERENCES clubs(id),
    group_key  TEXT        NOT NULL CHECK (group_key IN ('newcomers', 'members', 'deposit', 'general')),
    position   INTEGER     NOT NULL DEFAULT 0,
    question   TEXT        NOT NULL,
    answer     TEXT        NOT NULL,
    link_url   TEXT,
    link_label TEXT,
    published  BOOLEAN     NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX faqs_club_pub_group_pos ON faqs (club_id, published, group_key, position);
