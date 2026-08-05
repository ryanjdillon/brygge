-- Flat-priced items never need a boat for price calculation, so a boat
-- must not be forced on their faktura lines. Migration 000028 defaulted
-- requires_boat_selection=TRUE and only cleared it for slip_fee and
-- harbor_membership, leaving every other flat category (seasonal_rental,
-- guest, motorhome, room_hire, service, …) wrongly demanding a boat in
-- the single-faktura flow — e.g. a flat "Sommersesong" seasonal rental
-- for a member with no registered boat could not be invoiced.
--
-- Only tiered items (which resolve a beam/length tier from the boat)
-- genuinely require a boat. Clear the flag for all flat items and flip
-- the default so new flat items do not reintroduce the requirement; an
-- admin can still opt a specific flat item back in via the UI.

UPDATE price_items
   SET requires_boat_selection = FALSE
 WHERE pricing_kind = 'flat';

ALTER TABLE price_items ALTER COLUMN requires_boat_selection SET DEFAULT FALSE;
