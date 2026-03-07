ALTER TABLE slip_assignments DROP COLUMN IF EXISTS boat_id;
ALTER TABLE waiting_list_entries DROP COLUMN IF EXISTS boat_id;
ALTER TABLE boats
    DROP COLUMN IF EXISTS confirmed_at,
    DROP COLUMN IF EXISTS confirmed_by,
    DROP COLUMN IF EXISTS measurements_confirmed,
    DROP COLUMN IF EXISTS weight_kg,
    DROP COLUMN IF EXISTS model,
    DROP COLUMN IF EXISTS manufacturer,
    DROP COLUMN IF EXISTS boat_model_id;
DROP TABLE IF EXISTS boat_models;
