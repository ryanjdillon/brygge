ALTER TYPE document_visibility ADD VALUE IF NOT EXISTS 'slip_holder';

CREATE TABLE content_documents (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id      UUID        NOT NULL REFERENCES clubs(id),
    title        TEXT        NOT NULL,
    body_html    TEXT        NOT NULL DEFAULT '',
    visibility   TEXT        NOT NULL DEFAULT 'member' CHECK (visibility IN ('board', 'member', 'slip_holder')),
    published    BOOLEAN     NOT NULL DEFAULT false,
    revision     INTEGER     NOT NULL DEFAULT 0,
    published_at TIMESTAMPTZ,
    created_by   UUID        NOT NULL REFERENCES users(id),
    updated_by   UUID        NOT NULL REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX content_documents_club_vis_pub ON content_documents (club_id, visibility, published);
