ALTER TABLE price_items DROP CONSTRAINT IF EXISTS price_items_tier_dimension_consistency;
ALTER TABLE price_items DROP COLUMN IF EXISTS requires_boat_selection;
ALTER TABLE price_items DROP COLUMN IF EXISTS show_in_single;
ALTER TABLE price_items DROP COLUMN IF EXISTS show_in_batch;
ALTER TABLE price_items DROP COLUMN IF EXISTS tier_dimension;
ALTER TABLE price_items DROP COLUMN IF EXISTS pricing_kind;
