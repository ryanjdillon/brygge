-- A faktura has at most one invoice_sync journal entry (BRY-363). Booking
-- now happens at send time as well as from the sync/repair button, so two
-- concurrent paths could otherwise both book the same faktura. A voided
-- entry keeps the slot: a voided faktura is never re-booked.
CREATE UNIQUE INDEX IF NOT EXISTS idx_journal_entries_invoice_sync_once
    ON journal_entries (club_id, source_id)
    WHERE source = 'invoice_sync';
