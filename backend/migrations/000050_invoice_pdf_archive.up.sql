-- Faktura PDF archive. Norwegian bokføringsloven §13 requires invoice
-- documents to be retained for 5 years in the form the recipient was
-- shown. The DIL-364 in-place regenerate path overwrites
-- invoices.pdf_data; without this table the original bytes are lost.
-- Every regenerate (and future void / edit flows) inserts the prior
-- PDF here first inside a transaction so a failure leaves the
-- original PDF in place. See DIL-374.

CREATE TABLE invoice_pdf_archive (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id   UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    pdf_data     BYTEA NOT NULL,
    archived_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    -- Free-form reason so future write paths (void, manual edit,
    -- bulk recipient correction) can supply their own. Defaults to
    -- 'regenerate' for the current call site.
    reason       TEXT NOT NULL DEFAULT 'regenerate'
);

CREATE INDEX idx_invoice_pdf_archive_invoice
    ON invoice_pdf_archive (invoice_id, archived_at DESC);
