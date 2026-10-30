-- Payment filter groups (BRY-522): the chips of the Payments overview are
-- per-club groups of price items instead of the hard-coded kinds in
-- recentPaymentKindSQL. An order is in the first enabled group (by sort_order)
-- that holds one of its line items, or in the faktura built-in when it pays a
-- faktura, or else in the "other" built-in. A price item is in at most one
-- group. Rules: docs/adr/payments.md (PAY-15), backend/internal/paymentfilters.

CREATE TABLE IF NOT EXISTS payment_filter_groups (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id     UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    -- Stable id used in ?kind=, device storage, the admin API and tests. Set
    -- at create and never changed by a rename.
    slug        TEXT NOT NULL,
    -- Admin-entered label (club content, shown as typed in every UI locale).
    -- NULL only on a seeded row, which shows its translated label_key instead.
    name        TEXT,
    label_key   TEXT,
    icon        TEXT NOT NULL,
    sort_order  INT  NOT NULL,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    -- NULL = an item group (matched by its assigned price items);
    -- 'faktura' = matched by order_sources; 'other' = the fallback.
    builtin     TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_pfg_club_slug    ON payment_filter_groups (club_id, slug);
CREATE UNIQUE INDEX IF NOT EXISTS idx_pfg_club_builtin ON payment_filter_groups (club_id, builtin) WHERE builtin IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_pfg_club_name    ON payment_filter_groups (club_id, lower(name)) WHERE name IS NOT NULL;

CREATE TABLE IF NOT EXISTS payment_filter_group_items (
    -- PK on the item: a price item is in at most one group.
    price_item_id UUID PRIMARY KEY REFERENCES price_items(id) ON DELETE CASCADE,
    group_id      UUID NOT NULL REFERENCES payment_filter_groups(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_pfgi_group ON payment_filter_group_items (group_id);

-- "Is this price item sold" (an order line references it) is asked per item
-- by the group settings and the Payments page's nudge; order_lines had no index
-- on the item, and the price-item delete's reference count wants one too.
CREATE INDEX IF NOT EXISTS idx_order_lines_price_item ON order_lines (price_item_id) WHERE price_item_id IS NOT NULL;

-- The icon is format-checked only: the allow-list lives in code
-- (paymentfilters.IconAllowList), so adding an icon needs no migration.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'pfg_slug_format') THEN
        ALTER TABLE payment_filter_groups ADD CONSTRAINT pfg_slug_format
            CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,39}$');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'pfg_name_len') THEN
        ALTER TABLE payment_filter_groups ADD CONSTRAINT pfg_name_len
            CHECK (name IS NULL OR (length(btrim(name)) BETWEEN 1 AND 40 AND name = btrim(name)));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'pfg_label') THEN
        ALTER TABLE payment_filter_groups ADD CONSTRAINT pfg_label
            CHECK (name IS NOT NULL OR label_key IS NOT NULL);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'pfg_label_key') THEN
        ALTER TABLE payment_filter_groups ADD CONSTRAINT pfg_label_key
            CHECK (label_key IS NULL OR label_key IN ('guest', 'motorhome', 'slipping', 'faktura', 'other'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'pfg_builtin') THEN
        ALTER TABLE payment_filter_groups ADD CONSTRAINT pfg_builtin
            CHECK (builtin IS NULL OR builtin IN ('faktura', 'other'));
    END IF;
    -- A built-in's slug is its kind, so the classifier and the SPA can name it.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'pfg_builtin_slug') THEN
        ALTER TABLE payment_filter_groups ADD CONSTRAINT pfg_builtin_slug
            CHECK (builtin IS NULL OR slug = builtin);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'pfg_other_enabled') THEN
        ALTER TABLE payment_filter_groups ADD CONSTRAINT pfg_other_enabled
            CHECK (builtin IS DISTINCT FROM 'other' OR enabled);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'pfg_icon_format') THEN
        ALTER TABLE payment_filter_groups ADD CONSTRAINT pfg_icon_format
            CHECK (icon ~ '^[a-z][a-z0-9-]{0,39}$');
    END IF;
END
$$;

-- Backfill. This ports the rules the overview had before (guest, motorhome and
-- the interim "slipping" name rule) so nothing changes at deploy. It is derived
-- from each club's own price items: a club none of whose items match gets no
-- such group, and nothing is inserted from one club's practice (PLAT-1).

-- The two built-ins exist for every club.
INSERT INTO payment_filter_groups (club_id, slug, label_key, icon, sort_order, builtin)
SELECT c.id, 'faktura', 'faktura', 'file-text', 40, 'faktura' FROM clubs c
ON CONFLICT DO NOTHING;

INSERT INTO payment_filter_groups (club_id, slug, label_key, icon, sort_order, builtin)
SELECT c.id, 'other', 'other', 'circle-dollar-sign', 1000, 'other' FROM clubs c
ON CONFLICT DO NOTHING;

-- Item groups, only for a club with a price item (active or archived) the old
-- rule matched.
INSERT INTO payment_filter_groups (club_id, slug, label_key, icon, sort_order)
SELECT DISTINCT pi.club_id, 'guest', 'guest', 'sailboat', 10
  FROM price_items pi
 WHERE pi.category LIKE '%guest%'
ON CONFLICT DO NOTHING;

INSERT INTO payment_filter_groups (club_id, slug, label_key, icon, sort_order)
SELECT DISTINCT pi.club_id, 'motorhome', 'motorhome', 'van', 20
  FROM price_items pi
 WHERE pi.category LIKE '%motorhome%' OR pi.category LIKE '%bobil%'
ON CONFLICT DO NOTHING;

INSERT INTO payment_filter_groups (club_id, slug, label_key, icon, sort_order)
SELECT DISTINCT pi.club_id, 'slipping', 'slipping', 'forklift', 30
  FROM price_items pi
 WHERE pi.name ILIKE '%slipping%'
ON CONFLICT DO NOTHING;

-- Assign the items in the old priority order: an item matching two rules lands
-- in the first, as the old CASE did.
INSERT INTO payment_filter_group_items (price_item_id, group_id)
SELECT pi.id, g.id
  FROM price_items pi
  JOIN payment_filter_groups g ON g.club_id = pi.club_id AND g.slug = 'guest' AND g.builtin IS NULL
 WHERE pi.category LIKE '%guest%'
ON CONFLICT (price_item_id) DO NOTHING;

INSERT INTO payment_filter_group_items (price_item_id, group_id)
SELECT pi.id, g.id
  FROM price_items pi
  JOIN payment_filter_groups g ON g.club_id = pi.club_id AND g.slug = 'motorhome' AND g.builtin IS NULL
 WHERE pi.category LIKE '%motorhome%' OR pi.category LIKE '%bobil%'
ON CONFLICT (price_item_id) DO NOTHING;

INSERT INTO payment_filter_group_items (price_item_id, group_id)
SELECT pi.id, g.id
  FROM price_items pi
  JOIN payment_filter_groups g ON g.club_id = pi.club_id AND g.slug = 'slipping' AND g.builtin IS NULL
 WHERE pi.name ILIKE '%slipping%'
ON CONFLICT (price_item_id) DO NOTHING;

-- The tables hold club labels and codes only, no PII, so the read-only
-- developer role reads them raw.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'brygge_dev_ro') THEN
        EXECUTE 'GRANT SELECT ON payment_filter_groups, payment_filter_group_items TO brygge_dev_ro';
    END IF;
END
$$;
