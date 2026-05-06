-- Multi-line + custom-line support (DIL-259).
--
-- - account_id ties each line to a chart-of-accounts entry so journal
--   postings can stay correct for custom lines (e.g. membership dues
--   billed alongside a slip rental). Nullable for back-compat with
--   pre-existing rows; the API enforces "required for custom lines".
-- - sub_description is rendered on a smaller second line in the PDF
--   for boat/slip context (also used by bulk fakturas).
-- - price_item_id links a line to its price-list entry when one was
--   used; null on custom lines.

ALTER TABLE invoice_lines ADD COLUMN account_id     UUID REFERENCES accounts(id);
ALTER TABLE invoice_lines ADD COLUMN sub_description TEXT NOT NULL DEFAULT '';
ALTER TABLE invoice_lines ADD COLUMN price_item_id  UUID REFERENCES price_items(id);

CREATE INDEX IF NOT EXISTS idx_invoice_lines_account ON invoice_lines(account_id);
CREATE INDEX IF NOT EXISTS idx_invoice_lines_price_item ON invoice_lines(price_item_id);
