-- Add club financial fields for invoice generation
ALTER TABLE clubs ADD COLUMN IF NOT EXISTS org_number TEXT;
ALTER TABLE clubs ADD COLUMN IF NOT EXISTS address TEXT;
ALTER TABLE clubs ADD COLUMN IF NOT EXISTS bank_account TEXT;

CREATE TABLE invoices (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    club_id         UUID NOT NULL REFERENCES clubs(id) ON DELETE CASCADE,
    payment_id      UUID REFERENCES payments(id),
    user_id         UUID NOT NULL REFERENCES users(id),
    invoice_number  SERIAL,
    kid_number      TEXT NOT NULL,
    issue_date      DATE NOT NULL DEFAULT CURRENT_DATE,
    due_date        DATE NOT NULL,
    total_amount    NUMERIC(12,2) NOT NULL,
    pdf_data        BYTEA,
    sent_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE invoice_lines (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id  UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    description TEXT NOT NULL,
    quantity    INTEGER NOT NULL DEFAULT 1,
    unit_price  NUMERIC(12,2) NOT NULL,
    line_total  NUMERIC(12,2) NOT NULL
);

CREATE UNIQUE INDEX idx_invoices_kid ON invoices (club_id, kid_number);
CREATE INDEX idx_invoices_club_date ON invoices (club_id, issue_date DESC);
CREATE INDEX idx_invoices_user ON invoices (user_id);
