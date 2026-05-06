-- price_items.audience controls who a given price applies to and so
-- which column on the public pricing page it should render in.
--
--   'all'         — same price for everyone (default; current behavior)
--   'member'      — member-only pricing
--   'non_member'  — non-member / guest pricing
--
-- Two rows in the same category with the same name but different
-- audience express "X for members" vs "X for non-members" — that's how
-- the pricing page builds a side-by-side member/non-member comparison
-- without needing a parent/child relationship in the schema.

ALTER TABLE price_items ADD COLUMN audience TEXT NOT NULL DEFAULT 'all'
    CHECK (audience IN ('all', 'member', 'non_member'));
