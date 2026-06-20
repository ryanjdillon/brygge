-- Only safe to run before the data migration tool has nulled any
-- pdf_data rows; after that, re-adding NOT NULL would fail.
ALTER TABLE invoice_pdf_archive
    ALTER COLUMN pdf_data SET NOT NULL;

ALTER TABLE invoice_pdf_archive
    DROP COLUMN IF EXISTS s3_key;

ALTER TABLE invoices
    DROP COLUMN IF EXISTS s3_key;
