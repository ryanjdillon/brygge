-- Down only removes 1930 entries that have no journal_lines posted
-- against them — never drop an account that's already in use.
DELETE FROM accounts a
WHERE a.code = '1930'
  AND NOT EXISTS (
    SELECT 1 FROM journal_lines jl
    JOIN journal_entries je ON je.id = jl.entry_id
    WHERE jl.account_code = '1930' AND je.club_id = a.club_id
  );
