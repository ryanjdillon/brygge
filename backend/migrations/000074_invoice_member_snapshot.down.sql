ALTER TABLE invoices
  DROP COLUMN IF EXISTS member_email,
  DROP COLUMN IF EXISTS member_name;
