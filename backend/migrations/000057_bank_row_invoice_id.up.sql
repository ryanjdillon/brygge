ALTER TABLE bank_import_rows
    ADD COLUMN IF NOT EXISTS invoice_id UUID REFERENCES invoices(id);

CREATE INDEX IF NOT EXISTS idx_bank_import_rows_invoice_id
    ON bank_import_rows (invoice_id)
    WHERE invoice_id IS NOT NULL;
