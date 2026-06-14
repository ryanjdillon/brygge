-- Down is best-effort: we re-seed the 1930 chart entry for any club
-- that used to have one (using bank_imports as the witness — the
-- least-lossy heuristic), but the original gl_code on
-- club_bank_accounts and bank_imports can't be reliably restored
-- because we can't distinguish "was 1930, migrated to 1925" from
-- "was always 1925." Journal lines on 1925 stay there too.
-- Treat this down as "make 1930 exist again" only.

INSERT INTO accounts (club_id, code, name, account_type, parent_code, is_system, is_active, mva_eligible, description, sort_order)
SELECT DISTINCT
    bi.club_id,
    '1930',
    'Bankkonto annet',
    'asset',
    '1000',
    true,
    true,
    'not_applicable',
    '',
    37
FROM bank_imports bi
WHERE bi.bank_account_code = '1925'
ON CONFLICT (club_id, code) DO NOTHING;
