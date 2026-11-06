-- BRY-615: Stalwart sends get their delivery outcome from the mail journal.
-- A periodic sweep folds the journal for each recent Stalwart row and writes
-- the result into status; these columns hold the remote answer and the
-- sweep's bookkeeping. All nullable, nothing backfilled. Retention and
-- erasure delete whole email_sends rows, so they cover these columns too.

-- The remote server's answer for the outcome ("550 5.4.1 ..."), capped by
-- the writer at 500 bytes, cut on a rune boundary.
ALTER TABLE email_sends ADD COLUMN IF NOT EXISTS outcome_detail TEXT;
-- When the sweep last read the journal for this row (back-off).
ALTER TABLE email_sends ADD COLUMN IF NOT EXISTS outcome_checked_at TIMESTAMPTZ;
-- When the journal showed a terminal event; the sweep stops looking then.
ALTER TABLE email_sends ADD COLUMN IF NOT EXISTS outcome_final_at TIMESTAMPTZ;

-- The sweep's candidate scan: recent Stalwart rows without a final outcome.
CREATE INDEX IF NOT EXISTS idx_email_sends_stalwart_open
    ON email_sends (created_at)
    WHERE provider = 'stalwart' AND outcome_final_at IS NULL;

-- The sweep's per-recipient read and the member bounce list both match
-- sends by address, case-insensitively.
CREATE INDEX IF NOT EXISTS idx_email_sends_lower_recipient
    ON email_sends (lower(recipient), created_at);
