-- Make pricing items express tiering and applicability declaratively
-- instead of relying on the implicit "is metadata.beam_min set" check
-- and the implicit category convention. Drives the redesign in
-- DIL-262.
--
-- pricing_kind: 'flat' | 'tiered'. When tiered, multiple price_items
--   rows share a category and each row covers a min/max range on
--   tier_dimension.
-- tier_dimension: 'beam' | 'length'. NULL when pricing_kind='flat'.
-- show_in_batch / show_in_single: which faktura flow this item is
--   eligible for. Default safe = single only; admins explicitly opt in
--   to batch.
-- requires_boat_selection: when TRUE the admin must pick a specific
--   boat at faktura time (per-line boat_id). When FALSE the boat is
--   auto-resolved from the user's active permanent slip assignment.

ALTER TABLE price_items ADD COLUMN pricing_kind TEXT NOT NULL DEFAULT 'flat'
    CHECK (pricing_kind IN ('flat', 'tiered'));
ALTER TABLE price_items ADD COLUMN tier_dimension TEXT
    CHECK (tier_dimension IS NULL OR tier_dimension IN ('beam', 'length'));
ALTER TABLE price_items ADD COLUMN show_in_batch BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE price_items ADD COLUMN show_in_single BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE price_items ADD COLUMN requires_boat_selection BOOLEAN NOT NULL DEFAULT TRUE;

-- Backfill: tiered items already in production carry beam_min in their
-- metadata JSON. Flag them so the legacy detection ("metadata has
-- beam_min") becomes a one-time backfill instead of a runtime check.
UPDATE price_items
   SET pricing_kind   = 'tiered',
       tier_dimension = 'beam'
 WHERE metadata ? 'beam_min';

-- The two pre-existing "bulk" categories — harbor_membership and
-- slip_fee — were always meant for the batch flow with auto-resolved
-- boats from slip assignments. Carry that forward.
UPDATE price_items
   SET show_in_batch          = TRUE,
       requires_boat_selection = FALSE
 WHERE category IN ('harbor_membership', 'slip_fee');

-- Consistency invariant: tiered ⇒ tier_dimension set; flat ⇒ NULL.
ALTER TABLE price_items ADD CONSTRAINT price_items_tier_dimension_consistency
    CHECK (
        (pricing_kind = 'tiered' AND tier_dimension IS NOT NULL)
        OR
        (pricing_kind = 'flat' AND tier_dimension IS NULL)
    );
