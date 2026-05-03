-- Idempotent pricing/resources seed for an existing club.
-- Mirrors the slip-fee tiers, price catalog, and booking resources from
-- backend/cmd/seed/main.go so prod can be backfilled without running the
-- demo seeder (which would also create demo users, slips, boats, etc.).
--
-- Run against prod:
--   psql "$DATABASE_URL" -v club_slug=klokkarvik -f backend/scripts/seed_pricing.sql
--
-- Safe to re-run: every INSERT skips rows whose (club_id, name) already
-- exists, so existing prices are NEVER overwritten. To change a price,
-- edit it in the admin UI or via a targeted UPDATE.

\set ON_ERROR_STOP on

BEGIN;

-- Pricing catalog
WITH c AS (
    SELECT id FROM clubs WHERE slug = :'club_slug'
)
INSERT INTO price_items (club_id, category, name, description, amount, unit,
                         installments_allowed, max_installments, metadata, sort_order)
SELECT c.id, v.category, v.name, v.description, v.amount, v.unit,
       v.installments, v.max_install, v.metadata::jsonb, v.sort_order
FROM c, (VALUES
    ('slip_fee',          'Plassleie ≤ 2.5m bredde',     'Årlig plassleie basert på båtbredde',           6000::numeric, 'year',   false, 1,  '{"beam_min":0.0,"beam_max":2.5}', 20),
    ('slip_fee',          'Plassleie 2.5–3.5m bredde',   'Årlig plassleie basert på båtbredde',           8500::numeric, 'year',   false, 1,  '{"beam_min":2.5,"beam_max":3.5}', 21),
    ('slip_fee',          'Plassleie 3.5–4.5m bredde',   'Årlig plassleie basert på båtbredde',          12000::numeric, 'year',   false, 1,  '{"beam_min":3.5,"beam_max":4.5}', 22),
    ('slip_fee',          'Plassleie > 4.5m bredde',     'Årlig plassleie basert på båtbredde',          15000::numeric, 'year',   false, 1,  '{"beam_min":4.5,"beam_max":99}',  23),
    ('harbor_membership', 'Harbor Membership',           'One-time harbor infrastructure equity payment', 50000::numeric, 'once',   true,  12, '{}',                                                              10),
    ('seasonal_rental',   'Sommersesong',                'Sesongplass sommer',                             6000::numeric, 'season', false, 1,  '{"season":"summer","period_start":"05-01","period_end":"09-30"}', 30),
    ('seasonal_rental',   'Vintersesong',                'Sesongplass vinter',                             4000::numeric, 'season', false, 1,  '{"season":"winter","period_start":"10-01","period_end":"04-30"}', 31),
    ('guest',             'Gjesteplass per døgn',        'Gjesteplass ved hovedbrygga',                     250::numeric, 'day',    false, 1,  '{}',                                                              40),
    ('motorhome',         'Bobilplass per døgn',         'Bobilparkering med strøm',                        300::numeric, 'day',    false, 1,  '{}',                                                              50),
    ('room_hire',         'Klubbhuset',                  'Klubbhus med kjøkken, per dag',                  1500::numeric, 'day',    false, 1,  '{}',                                                              60),
    ('service',           'Kran – opp/utsett',           'Bruk av kran for sjøsetting/opptak',             1200::numeric, 'once',   false, 1,  '{}',                                                              70),
    ('service',           'Strøm vinter',                'Strømtilkobling gjennom vinteren',               2000::numeric, 'season', false, 1,  '{"season":"winter","period_start":"10-01","period_end":"04-30"}', 71)
) AS v(category, name, description, amount, unit, installments, max_install, metadata, sort_order)
WHERE NOT EXISTS (
    SELECT 1 FROM price_items pi
     WHERE pi.club_id = c.id AND pi.name = v.name
);

-- Booking resources (guest slips, bobil, klubbhus)
WITH c AS (
    SELECT id FROM clubs WHERE slug = :'club_slug'
)
INSERT INTO resources (club_id, type, name, description, unit, capacity, price_per_unit)
SELECT c.id, v.typ::resource_type, v.name, v.description, v.unit, v.capacity, v.price
FROM c, (VALUES
    ('guest_slip',     'Gjesteplass A', 'Gjesteplass ved hovedbrygga', 'night', 5,  250::numeric),
    ('guest_slip',     'Gjesteplass B', 'Gjesteplass ved nordbrygga',  'night', 3,  200::numeric),
    ('motorhome_spot', 'Bobilplass',    'Bobilparkering med strøm',    'night', 4,  300::numeric),
    ('club_room',      'Klubbhuset',    'Klubbhuset med kjøkken',      'day',   1, 1500::numeric)
) AS v(typ, name, description, unit, capacity, price)
WHERE NOT EXISTS (
    SELECT 1 FROM resources r
     WHERE r.club_id = c.id AND r.name = v.name
);

COMMIT;

\echo 'Price items now in catalog:'
SELECT category, name, amount, unit
  FROM price_items
 WHERE club_id = (SELECT id FROM clubs WHERE slug = :'club_slug')
 ORDER BY sort_order, name;

\echo 'Booking resources:'
SELECT type, name, capacity, price_per_unit
  FROM resources
 WHERE club_id = (SELECT id FROM clubs WHERE slug = :'club_slug')
 ORDER BY name;
