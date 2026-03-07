-- =============================================================================
-- Dugnad tracking: task participation, hour tracking, project-event links,
-- shopping lists
-- =============================================================================

-- Extend tasks table with dugnad fields
ALTER TABLE tasks
    ADD COLUMN estimated_hours  NUMERIC,
    ADD COLUMN actual_hours     NUMERIC,
    ADD COLUMN ansvarlig_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN max_collaborators INT NOT NULL DEFAULT 5,
    ADD COLUMN materials        JSONB NOT NULL DEFAULT '[]';

-- Link projects to dugnad calendar events
CREATE TABLE project_events (
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    event_id    UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, event_id)
);

-- Task participation (join/leave tasks)
CREATE TABLE task_participants (
    task_id     UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role        TEXT NOT NULL DEFAULT 'collaborator',
    hours       NUMERIC,
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, user_id)
);

CREATE INDEX idx_task_participants_user ON task_participants(user_id);

-- Club dugnad hour requirement
ALTER TABLE clubs
    ADD COLUMN required_dugnad_hours NUMERIC NOT NULL DEFAULT 0;

-- Shopping lists
CREATE TABLE shopping_lists (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_by  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    shared_with UUID REFERENCES users(id) ON DELETE SET NULL,
    status      TEXT NOT NULL DEFAULT 'active',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_shopping_lists_club ON shopping_lists(club_id);

CREATE TABLE shopping_list_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    list_id         UUID NOT NULL REFERENCES shopping_lists(id) ON DELETE CASCADE,
    task_id         UUID REFERENCES tasks(id) ON DELETE SET NULL,
    item            TEXT NOT NULL,
    quantity        NUMERIC,
    unit            TEXT NOT NULL DEFAULT '',
    est_cost        NUMERIC,
    checked         BOOLEAN NOT NULL DEFAULT false,
    sort_order      INT NOT NULL DEFAULT 0
);

CREATE INDEX idx_shopping_list_items_list ON shopping_list_items(list_id);
