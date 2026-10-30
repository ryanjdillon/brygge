-- A reference period keeps a year's data for comparison and momskompensasjon
-- while another system holds its books of record (BRY-418). Orthogonal to
-- status: the year stays open for expense bookings until momskomp is filed,
-- and stays reference after it is closed. No backfill: a club marks a year
-- through the UI, so the flag starts false everywhere.
ALTER TABLE fiscal_periods
    ADD COLUMN IF NOT EXISTS reference_only BOOLEAN NOT NULL DEFAULT false;
