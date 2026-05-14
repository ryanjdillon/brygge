-- Per-line boat reference so dedup and reporting can distinguish
-- multiple slip-fee lines on the same invoice for a multi-boat
-- slip-holder. NULL for category-less lines (dues, custom, etc).

ALTER TABLE invoice_lines
  ADD COLUMN boat_id UUID REFERENCES boats(id) ON DELETE SET NULL;

CREATE INDEX idx_invoice_lines_boat ON invoice_lines(boat_id) WHERE boat_id IS NOT NULL;
