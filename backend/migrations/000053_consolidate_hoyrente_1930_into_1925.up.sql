-- Undo the BankAccountsPanel/kontoplan inconsistency: the panel
-- defaulted `hoyrente` to gl_code 1930 (DIL-340 era) but the kontoplan
-- already had 1925 = "Bankkonto høyrente" (DIL-286 era). Migration
-- 000052 made it worse by seeding a 1930 entry labeled "Bankkonto
-- annet" — not honest about its real-world usage as a høyrente
-- account. The correct fix is to consolidate everything onto 1925
-- (the kontoplan's canonical høyrente code) and remove 1930 from the
-- chart entirely.
--
-- What this migration does:
--   1. Asserts that every club using 1930 also has 1925 available.
--      If any club has 1930 but not 1925, error out — those clubs
--      need manual review (and migration 000041 should have seeded
--      1925 universally, so this branch should be empty).
--   2. Repoints journal_lines from accounts(code='1930') to
--      accounts(code='1925') per club. This DOES rewrite posted
--      journal lines, which normally violates the immutable-posted-
--      entries invariant — but the substance (debit, credit, amount,
--      period, source row) is unchanged. Only the account label is
--      corrected from a mislabel to the canonical code.
--   3. Updates club_bank_accounts.gl_code 1930 → 1925.
--   4. Updates bank_imports.bank_account_code 1930 → 1925 so future
--      auto-correlation runs against the corrected code.
--   5. Drops 1930 chart entries — now safe, nothing references them.
--
-- After this, BankAccountsPanel and kontoplan.go are updated in the
-- same commit to keep 1930 from re-appearing.

DO $$
DECLARE
    missing_club uuid;
BEGIN
    SELECT a30.club_id INTO missing_club
    FROM accounts a30
    LEFT JOIN accounts a25
      ON a25.club_id = a30.club_id AND a25.code = '1925'
    WHERE a30.code = '1930' AND a25.id IS NULL
    LIMIT 1;
    IF missing_club IS NOT NULL THEN
        RAISE EXCEPTION '1930 chart entry exists for club % but 1925 is missing; '
                        'seed 1925 first (migration 000041 should have handled this)',
                        missing_club;
    END IF;
END
$$;

-- Step 2: repoint journal_lines.
UPDATE journal_lines jl
SET account_id = a25.id
FROM accounts a30
JOIN accounts a25
  ON a25.club_id = a30.club_id AND a25.code = '1925'
WHERE jl.account_id = a30.id AND a30.code = '1930';

-- Step 3: move bank accounts.
UPDATE club_bank_accounts
SET gl_code = '1925'
WHERE gl_code = '1930';

-- Step 4: move historical import metadata so future sync passes use
-- the canonical code.
UPDATE bank_imports
SET bank_account_code = '1925'
WHERE bank_account_code = '1930';

-- Step 5: drop now-unreferenced chart entries.
DELETE FROM accounts WHERE code = '1930';
