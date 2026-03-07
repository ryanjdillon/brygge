-- =============================================================================
-- Boat model registry and measurement confirmation
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

-- Enhance boats table with model link and confirmation workflow
ALTER TABLE boats
    ADD COLUMN boat_model_id          UUID REFERENCES boat_models(id),
    ADD COLUMN manufacturer           TEXT NOT NULL DEFAULT '',
    ADD COLUMN model                  TEXT NOT NULL DEFAULT '',
    ADD COLUMN weight_kg              NUMERIC,
    ADD COLUMN measurements_confirmed BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN confirmed_by           UUID REFERENCES users(id),
    ADD COLUMN confirmed_at           TIMESTAMPTZ;

-- Link waiting list entries to a specific boat
ALTER TABLE waiting_list_entries
    ADD COLUMN boat_id UUID REFERENCES boats(id) ON DELETE SET NULL;

-- Link slip assignments to a specific boat
ALTER TABLE slip_assignments
    ADD COLUMN boat_id UUID REFERENCES boats(id) ON DELETE SET NULL;
