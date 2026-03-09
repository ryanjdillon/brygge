-- =============================================================================
-- Reservation system: resource units, cancellation policies, slip sharing,
-- club settings, and booking enhancements
-- =============================================================================

BEGIN;

-- =============================================================================
-- 1. Extend enums
-- =============================================================================

ALTER TYPE resource_type ADD VALUE IF NOT EXISTS 'seasonal_rental';
ALTER TYPE resource_type ADD VALUE IF NOT EXISTS 'slip_hoist';
ALTER TYPE resource_type ADD VALUE IF NOT EXISTS 'shared_slip';

ALTER TYPE booking_status ADD VALUE IF NOT EXISTS 'completed';
ALTER TYPE booking_status ADD VALUE IF NOT EXISTS 'no_show';

COMMIT;

-- New transaction after enum additions (PG requires this)
BEGIN;

-- =============================================================================
-- 2. resource_units — individual bookable units within a resource
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
-- 3. resource_cancellation_policies — per-resource cancellation rules
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

-- =============================================================================
-- 4. slip_shares — member unavailability windows for slip sharing
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
-- 5. slip_share_rebates — per-booking rebate records
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

-- =============================================================================
-- 6. club_settings — configurable values for hoist, seasons, rebates
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
-- 7. Extend bookings table
-- =============================================================================

ALTER TABLE bookings
    ADD COLUMN resource_unit_id UUID REFERENCES resource_units(id) ON DELETE SET NULL,
    ADD COLUMN boat_length_m    NUMERIC,
    ADD COLUMN boat_beam_m      NUMERIC,
    ADD COLUMN boat_draft_m     NUMERIC;

CREATE INDEX idx_bookings_resource_unit ON bookings(resource_unit_id);

-- =============================================================================
-- 8. Seed default club settings for existing clubs
-- =============================================================================

INSERT INTO club_settings (club_id, key, value)
SELECT id, 'hoist_slot_duration_minutes', '120'::jsonb FROM clubs
ON CONFLICT (club_id, key) DO NOTHING;

INSERT INTO club_settings (club_id, key, value)
SELECT id, 'hoist_open_hour', '8'::jsonb FROM clubs
ON CONFLICT (club_id, key) DO NOTHING;

INSERT INTO club_settings (club_id, key, value)
SELECT id, 'hoist_close_hour', '20'::jsonb FROM clubs
ON CONFLICT (club_id, key) DO NOTHING;

INSERT INTO club_settings (club_id, key, value)
SELECT id, 'hoist_max_consecutive_slots', '2'::jsonb FROM clubs
ON CONFLICT (club_id, key) DO NOTHING;

INSERT INTO club_settings (club_id, key, value)
SELECT id, 'slip_share_rebate_pct', '25'::jsonb FROM clubs
ON CONFLICT (club_id, key) DO NOTHING;

INSERT INTO club_settings (club_id, key, value)
SELECT id, 'season_summer_start', '"04-01"'::jsonb FROM clubs
ON CONFLICT (club_id, key) DO NOTHING;

INSERT INTO club_settings (club_id, key, value)
SELECT id, 'season_summer_end', '"09-30"'::jsonb FROM clubs
ON CONFLICT (club_id, key) DO NOTHING;

INSERT INTO club_settings (club_id, key, value)
SELECT id, 'season_winter_start', '"10-01"'::jsonb FROM clubs
ON CONFLICT (club_id, key) DO NOTHING;

INSERT INTO club_settings (club_id, key, value)
SELECT id, 'season_winter_end', '"03-31"'::jsonb FROM clubs
ON CONFLICT (club_id, key) DO NOTHING;

-- =============================================================================
-- 9. Generate resource_units from existing resources
-- =============================================================================

INSERT INTO resource_units (resource_id, label, sort_order)
SELECT r.id, r.name || ' #' || gs.n, gs.n
FROM resources r
CROSS JOIN LATERAL generate_series(1, r.capacity) AS gs(n);

COMMIT;
