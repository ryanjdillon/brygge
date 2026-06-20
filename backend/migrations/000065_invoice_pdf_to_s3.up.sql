-- BRY-130: migrate invoice PDFs from DB BYTEA storage to S3.
-- Norwegian bokføringsloven §13 mandates 5-year retention; the S3
-- bucket (S3_BUCKET_LEGAL) carries a matching lifecycle policy.
--
-- s3_key is set by the migrate-invoices-to-s3 tool for existing rows
-- and by the API going forward for new invoices/archives.
-- pdf_data is retained as a fallback until the migration is confirmed
-- and then nulled by the same tool.

ALTER TABLE invoices
    ADD COLUMN s3_key TEXT;

ALTER TABLE invoice_pdf_archive
    ADD COLUMN s3_key TEXT;

-- Allow NULL so new archive rows can store the PDF in S3 only.
ALTER TABLE invoice_pdf_archive
    ALTER COLUMN pdf_data DROP NOT NULL;
