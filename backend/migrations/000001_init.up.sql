-- 000001_init.up.sql
-- Brygge baseline schema — multi-tenant harbour club management

BEGIN;

-- =============================================================================
-- Enum types
-- =============================================================================

CREATE TYPE user_role AS ENUM (
    'applicant',
    'member',
    'slip_holder',
    'board',
    'harbor_master',
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
    'harbor_membership',
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
    'motorhome_spot',
    'club_room',
    'other',
    'seasonal_rental',
    'slip_hoist',
    'shared_slip'
);

CREATE TYPE booking_status AS ENUM (
    'pending',
    'confirmed',
    'cancelled',
    'completed',
    'no_show'
);

CREATE TYPE event_tag AS ENUM (
    'regatta',
    'volunteer',
    'social',
    'agm',
    'other'
);

CREATE TYPE document_visibility AS ENUM (
    'member',
    'board'
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

CREATE TYPE order_status AS ENUM (
    'pending',
    'paid',
    'failed',
    'refunded'
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
    required_volunteer_hours NUMERIC NOT NULL DEFAULT 0,
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
-- 4. boat_models
-- =============================================================================

CREATE TABLE boat_models (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    manufacturer    TEXT NOT NULL,
    model           TEXT NOT NULL,
    year_from       INT,
    year_to         INT,
    length_m        NUMERIC,
    beam_m          NUMERIC,
    draft_m         NUMERIC,
    weight_kg       NUMERIC,
    boat_type       TEXT NOT NULL DEFAULT '',
    source          TEXT NOT NULL DEFAULT 'seed',
    external_id     TEXT NOT NULL DEFAULT '',
    checksum        TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_boat_models_manufacturer ON boat_models(manufacturer);
CREATE INDEX idx_boat_models_search ON boat_models
    USING gin (to_tsvector('simple', manufacturer || ' ' || model));
CREATE UNIQUE INDEX idx_boat_models_external ON boat_models(source, external_id)
    WHERE external_id != '';

-- =============================================================================
-- 5. boats
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
    boat_model_id       UUID REFERENCES boat_models(id),
    manufacturer        TEXT NOT NULL DEFAULT '',
    model               TEXT NOT NULL DEFAULT '',
    weight_kg           NUMERIC,
    measurements_confirmed BOOLEAN NOT NULL DEFAULT false,
    confirmed_by        UUID REFERENCES users(id),
    confirmed_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_boats_user_id ON boats(user_id);
CREATE INDEX idx_boats_club_id ON boats(club_id);
CREATE INDEX idx_boats_model ON boats(boat_model_id);

-- =============================================================================
-- 6. slips
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
-- 7. slip_assignments
-- =============================================================================

CREATE TABLE slip_assignments (
    id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slip_id                   UUID NOT NULL REFERENCES slips(id) ON DELETE CASCADE,
    user_id                   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    club_id                   UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    boat_id                   UUID REFERENCES boats(id) ON DELETE SET NULL,
    harbor_membership_amount  NUMERIC,
    harbor_membership_paid_at TIMESTAMPTZ,
    assigned_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    released_at               TIMESTAMPTZ
);

CREATE UNIQUE INDEX idx_slip_assignments_active
    ON slip_assignments(slip_id) WHERE released_at IS NULL;
CREATE INDEX idx_slip_assignments_club_id ON slip_assignments(club_id);
CREATE INDEX idx_slip_assignments_user_id ON slip_assignments(user_id);
CREATE INDEX idx_slip_assignments_boat ON slip_assignments(boat_id);

-- =============================================================================
-- 8. slip_fees
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
-- 9. waiting_list_entries
-- =============================================================================

CREATE TABLE waiting_list_entries (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    club_id         UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    boat_id         UUID REFERENCES boats(id) ON DELETE SET NULL,
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
CREATE INDEX idx_waiting_list_boat ON waiting_list_entries(boat_id);

-- =============================================================================
-- 10. payments
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
    due_date        DATE,
    description     TEXT NOT NULL DEFAULT '',
    paid_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_payments_club_id ON payments(club_id);
CREATE INDEX idx_payments_user_id ON payments(user_id);
CREATE INDEX idx_payments_status ON payments(club_id, status);
CREATE INDEX idx_payments_due_date ON payments(club_id, due_date) WHERE status = 'pending';

-- =============================================================================
-- 11. resources
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
-- 12. resource_units
-- =============================================================================

CREATE TABLE resource_units (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id  UUID NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    slip_id      UUID REFERENCES slips(id) ON DELETE SET NULL,
    label        TEXT NOT NULL,
    metadata     JSONB NOT NULL DEFAULT '{}',
    is_active    BOOLEAN NOT NULL DEFAULT true,
    sort_order   INT NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_resource_units_resource ON resource_units(resource_id);
CREATE UNIQUE INDEX idx_resource_units_active_slip ON resource_units(slip_id) WHERE slip_id IS NOT NULL;

-- =============================================================================
-- 13. resource_cancellation_policies
-- =============================================================================

CREATE TABLE resource_cancellation_policies (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id       UUID NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    free_cancel_hours INT NOT NULL DEFAULT 24,
    cancel_fee_pct    NUMERIC NOT NULL DEFAULT 0,
    no_refund_hours   INT NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (resource_id)
);

CREATE INDEX idx_cancellation_policies_resource ON resource_cancellation_policies(resource_id);

-- =============================================================================
-- 14. bookings
-- =============================================================================

CREATE TABLE bookings (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id     UUID NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    club_id         UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    resource_unit_id UUID REFERENCES resource_units(id) ON DELETE SET NULL,
    start_date      TIMESTAMPTZ NOT NULL,
    end_date        TIMESTAMPTZ NOT NULL,
    status          booking_status NOT NULL DEFAULT 'pending',
    guest_name      TEXT,
    guest_email     TEXT,
    guest_phone     TEXT,
    payment_id      UUID REFERENCES payments(id) ON DELETE SET NULL,
    notes           TEXT NOT NULL DEFAULT '',
    boat_length_m   NUMERIC,
    boat_beam_m     NUMERIC,
    boat_draft_m    NUMERIC,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_bookings_club_id ON bookings(club_id);
CREATE INDEX idx_bookings_resource_id ON bookings(resource_id);
CREATE INDEX idx_bookings_user_id ON bookings(user_id);
CREATE INDEX idx_bookings_resource_dates ON bookings(resource_id, start_date, end_date);
CREATE INDEX idx_bookings_resource_unit ON bookings(resource_unit_id);

-- =============================================================================
-- 15. slip_shares
-- =============================================================================

CREATE TABLE slip_shares (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slip_assignment_id UUID NOT NULL REFERENCES slip_assignments(id) ON DELETE CASCADE,
    club_id            UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    available_from     DATE NOT NULL,
    available_to       DATE NOT NULL,
    notes              TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT 'active',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT slip_shares_date_range CHECK (available_to > available_from)
);

CREATE INDEX idx_slip_shares_assignment ON slip_shares(slip_assignment_id);
CREATE INDEX idx_slip_shares_club_dates ON slip_shares(club_id, available_from, available_to);

-- =============================================================================
-- 16. slip_share_rebates
-- =============================================================================

CREATE TABLE slip_share_rebates (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slip_share_id   UUID NOT NULL REFERENCES slip_shares(id) ON DELETE CASCADE,
    booking_id      UUID NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    nights_rented   INT NOT NULL,
    rebate_pct      NUMERIC NOT NULL,
    rental_income   NUMERIC NOT NULL,
    rebate_amount   NUMERIC NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_slip_share_rebates_share ON slip_share_rebates(slip_share_id);
CREATE INDEX idx_slip_share_rebates_booking ON slip_share_rebates(booking_id);

-- =============================================================================
-- 17. club_settings
-- =============================================================================

CREATE TABLE club_settings (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id   UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    key       TEXT NOT NULL,
    value     JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (club_id, key)
);

CREATE INDEX idx_club_settings_club ON club_settings(club_id);

-- =============================================================================
-- 18. events
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
-- 19. documents
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
-- 20. document_comments
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
-- 21. projects
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
-- 22. tasks
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
    estimated_hours NUMERIC,
    actual_hours    NUMERIC,
    responsible_id  UUID REFERENCES users(id) ON DELETE SET NULL,
    max_collaborators INT NOT NULL DEFAULT 5,
    materials       JSONB NOT NULL DEFAULT '[]',
    created_by      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tasks_project_id ON tasks(project_id);
CREATE INDEX idx_tasks_club_id ON tasks(club_id);
CREATE INDEX idx_tasks_assignee_id ON tasks(assignee_id);

-- =============================================================================
-- 23. project_events
-- =============================================================================

CREATE TABLE project_events (
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    event_id    UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, event_id)
);

CREATE INDEX idx_project_events_project ON project_events(project_id);
CREATE INDEX idx_project_events_event ON project_events(event_id);

-- =============================================================================
-- 24. task_participants
-- =============================================================================

CREATE TABLE task_participants (
    task_id     UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role        TEXT NOT NULL DEFAULT 'collaborator',
    hours       NUMERIC,
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, user_id)
);

CREATE INDEX idx_task_participants_user ON task_participants(user_id);

-- =============================================================================
-- 25. shopping_lists
-- =============================================================================

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

-- =============================================================================
-- 26. shopping_list_items
-- =============================================================================

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
CREATE INDEX idx_shopping_list_items_task ON shopping_list_items(task_id);

-- =============================================================================
-- 27. feature_requests
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
-- 28. votes
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
-- 29. matrix_rooms
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
-- 30. broadcasts
-- =============================================================================

CREATE TABLE broadcasts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    subject     TEXT NOT NULL,
    body        TEXT NOT NULL,
    recipients  TEXT NOT NULL,
    sent_by     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sent_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_broadcasts_club_id ON broadcasts(club_id);
CREATE INDEX idx_broadcasts_sent_at ON broadcasts(club_id, sent_at);

-- =============================================================================
-- 31. price_items
-- =============================================================================

CREATE TABLE price_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id         UUID NOT NULL REFERENCES clubs(id),
    category        TEXT NOT NULL,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    amount          NUMERIC NOT NULL,
    currency        TEXT NOT NULL DEFAULT 'NOK',
    unit            TEXT NOT NULL DEFAULT 'once',
    installments_allowed BOOLEAN NOT NULL DEFAULT false,
    max_installments     INT NOT NULL DEFAULT 1,
    metadata        JSONB NOT NULL DEFAULT '{}',
    sort_order      INT NOT NULL DEFAULT 0,
    is_active       BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_price_items_club ON price_items(club_id);
CREATE INDEX idx_price_items_category ON price_items(club_id, category);

-- =============================================================================
-- 32. products
-- =============================================================================

CREATE TABLE products (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id         UUID NOT NULL REFERENCES clubs(id),
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    price           NUMERIC NOT NULL,
    currency        TEXT NOT NULL DEFAULT 'NOK',
    image_url       TEXT NOT NULL DEFAULT '',
    stock           INT NOT NULL DEFAULT 0,
    is_active       BOOLEAN NOT NULL DEFAULT true,
    sort_order      INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_products_club ON products(club_id);

-- =============================================================================
-- 33. product_variants
-- =============================================================================

CREATE TABLE product_variants (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id  UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    size        TEXT NOT NULL DEFAULT '',
    color       TEXT NOT NULL DEFAULT '',
    stock       INT NOT NULL DEFAULT 0,
    price_override NUMERIC,
    image_url   TEXT NOT NULL DEFAULT '',
    sort_order  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_product_variants_product ON product_variants(product_id);
CREATE UNIQUE INDEX idx_product_variants_unique ON product_variants(product_id, size, color);

-- =============================================================================
-- 34. orders
-- =============================================================================

CREATE TABLE orders (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id         UUID NOT NULL REFERENCES clubs(id),
    user_id         UUID REFERENCES users(id),
    guest_email     TEXT NOT NULL DEFAULT '',
    guest_name      TEXT NOT NULL DEFAULT '',
    status          order_status NOT NULL DEFAULT 'pending',
    total_amount    NUMERIC NOT NULL,
    currency        TEXT NOT NULL DEFAULT 'NOK',
    vipps_reference TEXT NOT NULL DEFAULT '',
    paid_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_orders_club ON orders(club_id);
CREATE INDEX idx_orders_user ON orders(user_id);
CREATE INDEX idx_orders_vipps ON orders(vipps_reference) WHERE vipps_reference != '';

-- =============================================================================
-- 35. order_lines
-- =============================================================================

CREATE TABLE order_lines (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id    UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    product_id  UUID REFERENCES products(id) ON DELETE SET NULL,
    price_item_id UUID REFERENCES price_items(id) ON DELETE SET NULL,
    variant_id  UUID REFERENCES product_variants(id) ON DELETE SET NULL,
    name        TEXT NOT NULL,
    quantity    INT NOT NULL DEFAULT 1,
    unit_price  NUMERIC NOT NULL,
    total_price NUMERIC NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_order_lines_order ON order_lines(order_id);
CREATE INDEX idx_order_lines_product ON order_lines(product_id);

-- =============================================================================
-- 36. map_markers
-- =============================================================================

CREATE TABLE map_markers (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    marker_type TEXT NOT NULL,
    label       TEXT NOT NULL DEFAULT '',
    lat         NUMERIC NOT NULL,
    lng         NUMERIC NOT NULL,
    sort_order  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_map_markers_club ON map_markers(club_id);

-- =============================================================================
-- 37. push_subscriptions
-- =============================================================================

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
CREATE INDEX idx_push_subscriptions_user ON push_subscriptions(user_id);

-- =============================================================================
-- 38. notification_preferences
-- =============================================================================

CREATE TABLE notification_preferences (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    club_id     UUID NOT NULL REFERENCES clubs(id),
    category    TEXT NOT NULL,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    PRIMARY KEY (user_id, club_id, category)
);

CREATE INDEX idx_notif_prefs_user_club ON notification_preferences(user_id, club_id);

-- =============================================================================
-- 39. notification_config
-- =============================================================================

CREATE TABLE notification_config (
    club_id     UUID NOT NULL REFERENCES clubs(id),
    category    TEXT NOT NULL,
    required    BOOLEAN NOT NULL DEFAULT false,
    lead_days   INT,
    PRIMARY KEY (club_id, category)
);

-- =============================================================================
-- 40. user_consents
-- =============================================================================

CREATE TABLE user_consents (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    club_id     UUID NOT NULL REFERENCES clubs(id),
    consent_type TEXT NOT NULL,
    version     TEXT NOT NULL,
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ
);

CREATE INDEX idx_user_consents_user ON user_consents(user_id);

-- =============================================================================
-- 41. deletion_requests
-- =============================================================================

CREATE TABLE deletion_requests (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id),
    club_id         UUID NOT NULL REFERENCES clubs(id),
    requested_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    grace_end       TIMESTAMPTZ NOT NULL,
    cancelled_at    TIMESTAMPTZ,
    processed_at    TIMESTAMPTZ,
    status          TEXT NOT NULL DEFAULT 'pending'
);

CREATE INDEX idx_deletion_requests_user ON deletion_requests(user_id);
CREATE INDEX idx_deletion_requests_club_status ON deletion_requests(club_id, status);

-- =============================================================================
-- 42. legal_documents
-- =============================================================================

CREATE TABLE legal_documents (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id),
    doc_type    TEXT NOT NULL,
    version     TEXT NOT NULL,
    content     TEXT NOT NULL,
    published_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =============================================================================
-- 43. revoked_tokens
-- =============================================================================

CREATE TABLE revoked_tokens (
    jti         TEXT PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id),
    revoked_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_revoked_tokens_expires ON revoked_tokens(expires_at);

-- =============================================================================
-- 44. audit_log (append-only)
-- =============================================================================

CREATE TABLE audit_log (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID REFERENCES clubs(id),
    actor_id    UUID REFERENCES users(id),
    actor_ip    TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL,
    resource    TEXT NOT NULL,
    resource_id TEXT,
    details     JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_log_club ON audit_log(club_id, created_at DESC);
CREATE INDEX idx_audit_log_actor ON audit_log(actor_id, created_at DESC);
CREATE INDEX idx_audit_log_resource ON audit_log(resource, resource_id);

-- =============================================================================
-- 45. refresh_tokens
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
