-- Backfill kontoplan entry 1930 "Bankkonto annet" for any club that
-- already has a `club_bank_accounts` row using gl_code = '1930' but
-- whose chart of accounts never received the corresponding entry —
-- typically because `BankAccountsPanel` (DIL-340) defaulted the
-- hoyrente role to 1930 even though the default kontoplan only seeds
-- 1920 + 1925.
--
-- Symptom this fixes: bank-statement KID matches against rows whose
-- import uses code 1930 silently fail in `syncKIDMatches` /
-- `ImportBankRows` because `CreateJournalEntry` rejects the line for
-- an FK violation on `accounts(club_id, code)`. The error path used
-- to `continue` without logging — see banksync.go follow-up for the
-- logging fix.
--
-- Narrow scope: only clubs that have an active 1930 bank account get
-- the chart entry. Clubs without 1930 usage keep a tidier chart.

INSERT INTO accounts (club_id, code, name, account_type, parent_code, is_system, is_active, mva_eligible, description, sort_order)
SELECT DISTINCT
    cba.club_id,
    '1930',
    'Bankkonto annet',
    'asset',
    '1000',
    true,
    true,
    'not_applicable',
    '',
    37
FROM club_bank_accounts cba
WHERE cba.gl_code = '1930'
  AND cba.archived_at IS NULL
ON CONFLICT (club_id, code) DO NOTHING;
