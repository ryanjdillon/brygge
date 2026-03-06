ALTER TABLE payments ADD COLUMN due_date DATE;
ALTER TABLE payments ADD COLUMN description TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_payments_due_date ON payments(club_id, due_date) WHERE status = 'pending';
