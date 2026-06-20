ALTER TABLE bank_import_rows
  ADD COLUMN refund_paired_with UUID
    REFERENCES bank_import_rows(id) ON DELETE SET NULL;

CREATE INDEX idx_bank_import_rows_refund_pending
  ON bank_import_rows (club_id)
  WHERE dismissed_reason IN ('double_payment', 'overpayment', 'refund_or_credit')
    AND refund_paired_with IS NULL;
