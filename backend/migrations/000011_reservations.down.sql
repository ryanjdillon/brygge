BEGIN;

ALTER TABLE bookings
    DROP COLUMN IF EXISTS resource_unit_id,
    DROP COLUMN IF EXISTS boat_length_m,
    DROP COLUMN IF EXISTS boat_beam_m,
    DROP COLUMN IF EXISTS boat_draft_m;

DROP TABLE IF EXISTS club_settings;
DROP TABLE IF EXISTS slip_share_rebates;
DROP TABLE IF EXISTS slip_shares;
DROP TABLE IF EXISTS resource_cancellation_policies;
DROP TABLE IF EXISTS resource_units;

-- Note: enum values cannot be removed in PostgreSQL without recreating the type.
-- The added values (seasonal_rental, slip_hoist, shared_slip, completed, no_show)
-- are left in place as they are harmless when unused.

COMMIT;
