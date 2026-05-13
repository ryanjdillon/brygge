-- Backfill kontoplan entry 1925 Bankkonto høyrente for clubs whose
-- chart of accounts was seeded before DIL-286 added this default.
-- New clubs get it automatically via SeedKontoplan; this fills the gap
-- for existing clubs so the bank-imports dropdown shows it without
-- requiring a manual re-seed click.

INSERT INTO accounts (club_id, code, name, account_type, parent_code, is_system, is_active, mva_eligible, description, sort_order)
SELECT
    c.id,
    '1925',
    'Bankkonto høyrente',
    'asset',
    '1000',
    true,
    true,
    'not_applicable',
    '',
    35
FROM clubs c
WHERE EXISTS (SELECT 1 FROM accounts WHERE club_id = c.id)
ON CONFLICT (club_id, code) DO NOTHING;
