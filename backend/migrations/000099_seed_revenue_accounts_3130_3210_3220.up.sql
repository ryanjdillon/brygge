-- Backfill revenue accounts 3130 Strøm, 3210 Bobil/campingplass and
-- 3220 Lokalleie for clubs whose chart of accounts was seeded before
-- they joined the default kontoplan. GL sync maps the electricity,
-- motorhome and room_hire price categories to these codes; without the
-- rows, CreateJournalEntry fails with "account not found" and the
-- invoice is skipped. New clubs get them via SeedKontoplan.

INSERT INTO accounts (club_id, code, name, account_type, parent_code, is_system, is_active, mva_eligible, description, sort_order)
SELECT c.id, v.code, v.name, 'revenue', '3000', true, true, 'not_applicable', v.description, v.sort_order
FROM clubs c
CROSS JOIN (VALUES
    ('3130', 'Strøm', 'Strømsalg til båtplasser og gjester', 105),
    ('3210', 'Bobil/campingplass', 'Bobil- og campingutleie, holdt adskilt fra gjestehavn fordi den kan ha en annen MVA-behandling ved registrering', 112),
    ('3220', 'Lokalleie', 'Utleie av klubbhus og lokaler', 114)
) AS v(code, name, description, sort_order)
WHERE EXISTS (SELECT 1 FROM accounts WHERE club_id = c.id)
ON CONFLICT (club_id, code) DO NOTHING;
