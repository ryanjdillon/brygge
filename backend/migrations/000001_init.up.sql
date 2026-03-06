-- 000001_init.up.sql
-- Brygge MVP schema — multi-tenant harbour club management

BEGIN;

-- =============================================================================
-- Enum types
-- =============================================================================

CREATE TYPE user_role AS ENUM (
    'applicant',
    'member',
    'slip_owner',
    'styre',
    'harbour_master',
    'treasurer',
    'admin'
);

CREATE TYPE slip_status AS ENUM (
    'vacant',
    'occupied',
    'reserved',
    'maintenance'
);

CREATE TYPE waiting_list_status AS ENUM (
    'active',
    'offered',
    'accepted',
    'expired',
    'withdrawn'
);

CREATE TYPE payment_type AS ENUM (
    'dues',
    'andel',
    'slip_fee',
    'booking',
    'merchandise'
);

CREATE TYPE payment_status AS ENUM (
    'pending',
    'completed',
    'failed',
    'refunded'
);

CREATE TYPE resource_type AS ENUM (
    'guest_slip',
    'bobil_spot',
    'club_room',
    'other'
);

CREATE TYPE booking_status AS ENUM (
    'pending',
    'confirmed',
    'cancelled'
);

CREATE TYPE event_tag AS ENUM (
    'regatta',
    'dugnad',
    'social',
    'agm',
    'other'
);

CREATE TYPE document_visibility AS ENUM (
    'member',
    'styre'
);

CREATE TYPE task_status AS ENUM (
    'todo',
    'in_progress',
    'done'
);

CREATE TYPE task_priority AS ENUM (
    'low',
    'medium',
    'high'
);

CREATE TYPE feature_request_status AS ENUM (
    'proposed',
    'reviewing',
    'accepted',
    'rejected',
    'done'
);

-- =============================================================================
-- 1. clubs
-- =============================================================================

CREATE TABLE clubs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug            TEXT NOT NULL UNIQUE,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    latitude        NUMERIC,
    longitude       NUMERIC,
    municipality_codes TEXT[] NOT NULL DEFAULT '{}',
    postal_codes    TEXT[] NOT NULL DEFAULT '{}',
    config          JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =============================================================================
-- 2. users
-- =============================================================================

CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id         UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    email           TEXT NOT NULL,
    password_hash   TEXT,
    vipps_sub       TEXT,
    full_name       TEXT NOT NULL,
    phone           TEXT NOT NULL DEFAULT '',
    address_line    TEXT NOT NULL DEFAULT '',
    postal_code     TEXT NOT NULL DEFAULT '',
    city            TEXT NOT NULL DEFAULT '',
    is_local        BOOLEAN NOT NULL DEFAULT false,
    local_override_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (club_id, email),
    UNIQUE (club_id, vipps_sub)
);

CREATE INDEX idx_users_club_id ON users(club_id);

-- =============================================================================
-- 3. user_roles
-- =============================================================================

CREATE TABLE user_roles (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    club_id         UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    role            user_role NOT NULL,
    granted_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    granted_by      UUID REFERENCES users(id) ON DELETE SET NULL,

    UNIQUE (user_id, club_id, role)
);

CREATE INDEX idx_user_roles_user_id ON user_roles(user_id);
CREATE INDEX idx_user_roles_club_id ON user_roles(club_id);

-- =============================================================================
-- 4. boats
-- =============================================================================

CREATE TABLE boats (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    club_id             UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    name                TEXT NOT NULL DEFAULT '',
    type                TEXT NOT NULL DEFAULT '',
    length_m            NUMERIC,
    beam_m              NUMERIC,
    draft_m             NUMERIC,
    registration_number TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_boats_user_id ON boats(user_id);
CREATE INDEX idx_boats_club_id ON boats(club_id);

-- =============================================================================
-- 5. slips
-- =============================================================================

CREATE TABLE slips (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    number      TEXT NOT NULL,
    section     TEXT NOT NULL DEFAULT '',
    length_m    NUMERIC,
    width_m     NUMERIC,
    depth_m     NUMERIC,
    status      slip_status NOT NULL DEFAULT 'vacant',
    map_x       NUMERIC,
    map_y       NUMERIC,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (club_id, number)
);

CREATE INDEX idx_slips_club_id ON slips(club_id);
CREATE INDEX idx_slips_club_status ON slips(club_id, status);

-- =============================================================================
-- 6. slip_assignments
-- =============================================================================

CREATE TABLE slip_assignments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slip_id         UUID NOT NULL REFERENCES slips(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    club_id         UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    andel_amount    NUMERIC,
    andel_paid_at   TIMESTAMPTZ,
    assigned_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    released_at     TIMESTAMPTZ
);

CREATE UNIQUE INDEX idx_slip_assignments_active
    ON slip_assignments(slip_id) WHERE released_at IS NULL;
CREATE INDEX idx_slip_assignments_club_id ON slip_assignments(club_id);
CREATE INDEX idx_slip_assignments_user_id ON slip_assignments(user_id);

-- =============================================================================
-- 7. slip_fees
-- =============================================================================

CREATE TABLE slip_fees (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slip_assignment_id  UUID NOT NULL REFERENCES slip_assignments(id) ON DELETE CASCADE,
    club_id             UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    year                INT NOT NULL,
    amount              NUMERIC NOT NULL,
    due_date            DATE NOT NULL,
    paid_at             TIMESTAMPTZ,
    payment_reference   TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_slip_fees_club_id ON slip_fees(club_id);
CREATE INDEX idx_slip_fees_assignment_id ON slip_fees(slip_assignment_id);
CREATE UNIQUE INDEX idx_slip_fees_assignment_year ON slip_fees(slip_assignment_id, year);

-- =============================================================================
-- 8. waiting_list_entries
-- =============================================================================

CREATE TABLE waiting_list_entries (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    club_id         UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    position        INT NOT NULL,
    is_local        BOOLEAN NOT NULL DEFAULT false,
    status          waiting_list_status NOT NULL DEFAULT 'active',
    offer_deadline  TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_waiting_list_club_id ON waiting_list_entries(club_id);
CREATE INDEX idx_waiting_list_club_position ON waiting_list_entries(club_id, position);
CREATE INDEX idx_waiting_list_user_id ON waiting_list_entries(user_id);

-- =============================================================================
-- 9. payments
-- =============================================================================

CREATE TABLE payments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id         UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type            payment_type NOT NULL,
    amount          NUMERIC NOT NULL,
    currency        TEXT NOT NULL DEFAULT 'NOK',
    vipps_reference TEXT NOT NULL DEFAULT '',
    status          payment_status NOT NULL DEFAULT 'pending',
    paid_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_payments_club_id ON payments(club_id);
CREATE INDEX idx_payments_user_id ON payments(user_id);
CREATE INDEX idx_payments_status ON payments(club_id, status);

-- =============================================================================
-- 10. resources
-- =============================================================================

CREATE TABLE resources (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id         UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    type            resource_type NOT NULL,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    capacity        INT NOT NULL DEFAULT 1,
    price_per_unit  NUMERIC NOT NULL DEFAULT 0,
    unit            TEXT NOT NULL DEFAULT 'night',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_resources_club_id ON resources(club_id);
CREATE INDEX idx_resources_club_type ON resources(club_id, type);

-- =============================================================================
-- 11. bookings
-- =============================================================================

CREATE TABLE bookings (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id     UUID NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    club_id         UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    start_date      TIMESTAMPTZ NOT NULL,
    end_date        TIMESTAMPTZ NOT NULL,
    status          booking_status NOT NULL DEFAULT 'pending',
    guest_name      TEXT,
    guest_email     TEXT,
    guest_phone     TEXT,
    payment_id      UUID REFERENCES payments(id) ON DELETE SET NULL,
    notes           TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_bookings_club_id ON bookings(club_id);
CREATE INDEX idx_bookings_resource_id ON bookings(resource_id);
CREATE INDEX idx_bookings_user_id ON bookings(user_id);
CREATE INDEX idx_bookings_resource_dates ON bookings(resource_id, start_date, end_date);

-- =============================================================================
-- 12. events
-- =============================================================================

CREATE TABLE events (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    location    TEXT NOT NULL DEFAULT '',
    start_time  TIMESTAMPTZ NOT NULL,
    end_time    TIMESTAMPTZ NOT NULL,
    tag         event_tag NOT NULL DEFAULT 'other',
    is_public   BOOLEAN NOT NULL DEFAULT true,
    created_by  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_events_club_id ON events(club_id);
CREATE INDEX idx_events_club_public ON events(club_id, is_public);
CREATE INDEX idx_events_start_time ON events(club_id, start_time);

-- =============================================================================
-- 13. documents
-- =============================================================================

CREATE TABLE documents (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id         UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    filename        TEXT NOT NULL,
    s3_key          TEXT NOT NULL,
    content_type    TEXT NOT NULL DEFAULT '',
    size_bytes      BIGINT NOT NULL DEFAULT 0,
    visibility      document_visibility NOT NULL DEFAULT 'member',
    uploaded_by     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_documents_club_id ON documents(club_id);
CREATE INDEX idx_documents_club_visibility ON documents(club_id, visibility);

-- =============================================================================
-- 14. document_comments
-- =============================================================================

CREATE TABLE document_comments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id     UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    club_id         UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    body            TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_document_comments_document_id ON document_comments(document_id);
CREATE INDEX idx_document_comments_club_id ON document_comments(club_id);

-- =============================================================================
-- 15. projects
-- =============================================================================

CREATE TABLE projects (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_by  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_projects_club_id ON projects(club_id);

-- =============================================================================
-- 16. tasks
-- =============================================================================

CREATE TABLE tasks (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    club_id         UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    assignee_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    status          task_status NOT NULL DEFAULT 'todo',
    priority        task_priority NOT NULL DEFAULT 'medium',
    due_date        DATE,
    created_by      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tasks_project_id ON tasks(project_id);
CREATE INDEX idx_tasks_club_id ON tasks(club_id);
CREATE INDEX idx_tasks_assignee_id ON tasks(assignee_id);

-- =============================================================================
-- 17. feature_requests
-- =============================================================================

CREATE TABLE feature_requests (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id         UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    status          feature_request_status NOT NULL DEFAULT 'proposed',
    submitted_by    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_feature_requests_club_id ON feature_requests(club_id);

-- =============================================================================
-- 18. votes
-- =============================================================================

CREATE TABLE votes (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    feature_request_id  UUID NOT NULL REFERENCES feature_requests(id) ON DELETE CASCADE,
    user_id             UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    value               INT NOT NULL CHECK (value IN (1, -1)),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (feature_request_id, user_id)
);

CREATE INDEX idx_votes_feature_request_id ON votes(feature_request_id);

-- =============================================================================
-- 19. matrix_rooms
-- =============================================================================

CREATE TABLE matrix_rooms (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    room_id     TEXT NOT NULL,
    name        TEXT NOT NULL,
    is_private  BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_matrix_rooms_club_id ON matrix_rooms(club_id);
CREATE UNIQUE INDEX idx_matrix_rooms_room_id ON matrix_rooms(room_id);

-- =============================================================================
-- 20. audit_log (append-only)
-- =============================================================================

CREATE TABLE audit_log (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id) ON DELETE RESTRICT,
    user_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    action      TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id   UUID NOT NULL,
    old_data    JSONB,
    new_data    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_log_club_id ON audit_log(club_id);
CREATE INDEX idx_audit_log_entity ON audit_log(entity_type, entity_id);
CREATE INDEX idx_audit_log_created_at ON audit_log(club_id, created_at);

-- =============================================================================
-- 21. refresh_tokens
-- =============================================================================

CREATE TABLE refresh_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ
);

CREATE INDEX idx_refresh_tokens_user_id ON refresh_tokens(user_id);

COMMIT;
