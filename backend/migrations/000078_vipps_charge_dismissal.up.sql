-- Per-charge Vipps reconciliation (BRY-227) needs an "Avvis" action for an
-- individual charge, mirroring the bank-row dismissal. A dismissed charge
-- drops out of the Tildel queue and no longer blocks its settlement's payout
-- auto-clear. Reasons reuse the bank-row dismissal vocabulary.
ALTER TABLE vipps_import_rows
  ADD COLUMN dismissed_at     TIMESTAMPTZ,
  ADD COLUMN dismissed_reason TEXT,
  ADD COLUMN dismissed_by     UUID REFERENCES users(id) ON DELETE SET NULL;

-- Unreconciled, non-dismissed belastning rows are the Tildel queue; index the
-- hot filter so the list + the payout-clear gate stay cheap.
CREATE INDEX idx_vipps_import_rows_open_charges
  ON vipps_import_rows(club_id, msn, booking_date)
  WHERE row_type = 'belastning' AND journal_entry_id IS NULL AND dismissed_at IS NULL;
