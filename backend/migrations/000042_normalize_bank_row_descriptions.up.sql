-- Collapse internal whitespace in existing bank_import_rows.description
-- so multi-line "Melding" content (e.g. Norsk Tipping invoice tables)
-- renders cleanly in the row listing. row_hash is recomputed using the
-- same recipe as BankRowHash() in the Go code so future re-uploads of
-- the same statement still dedupe correctly.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

WITH updated AS (
    SELECT
        id,
        trim(regexp_replace(description, '\s+', ' ', 'g')) AS new_description
    FROM bank_import_rows
    WHERE description ~ '[\r\n\t]' OR description ~ '  '
)
UPDATE bank_import_rows bir
SET description = u.new_description,
    row_hash = encode(
        public.digest(
            lower(
                to_char(bir.row_date, 'YYYY-MM-DD') || '|' ||
                trim(to_char(bir.amount, 'FM999999999990.00')) || '|' ||
                COALESCE(bir.reference, '') || '|' ||
                u.new_description || '|' ||
                COALESCE(bir.counterpart, '')
            ),
            'sha256'
        ),
        'hex'
    )
FROM updated u
WHERE bir.id = u.id;
