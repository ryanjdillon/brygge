-- BRY-558: a reversal bilag names the entry it reverses, so a ledger void can
-- refuse to void a reversal (which would re-instate the original booking and
-- leave a void of a void with no explicit link). Before this, the only trace
-- was the description "Reversering av bilag #N: ...".
ALTER TABLE journal_entries ADD COLUMN IF NOT EXISTS reverses_entry_id UUID REFERENCES journal_entries(id);

CREATE INDEX IF NOT EXISTS idx_journal_entries_reverses_entry_id
    ON journal_entries (reverses_entry_id) WHERE reverses_entry_id IS NOT NULL;

-- Backfill from the description convention, only where it names exactly one
-- voided entry of the same club and period whose number and description match
-- the reversal's text. Anything ambiguous stays unlinked.
UPDATE journal_entries r
   SET reverses_entry_id = m.orig_id
  FROM (
    SELECT r2.id AS rev_id, (array_agg(o.id))[1] AS orig_id
      FROM journal_entries r2
      JOIN journal_entries o
        ON o.club_id = r2.club_id
       AND o.fiscal_period_id = r2.fiscal_period_id
       AND o.status = 'voided'
       AND r2.description = 'Reversering av bilag #' || o.entry_number || ': ' || o.description
     WHERE r2.source = 'manual'
       AND r2.reverses_entry_id IS NULL
     GROUP BY r2.id
    HAVING count(*) = 1
  ) m
 WHERE r.id = m.rev_id
   AND r.reverses_entry_id IS NULL;
