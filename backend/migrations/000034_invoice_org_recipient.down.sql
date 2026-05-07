ALTER TABLE invoices DROP COLUMN IF EXISTS recipient_email;
ALTER TABLE invoices DROP COLUMN IF EXISTS recipient_their_ref;
ALTER TABLE invoices DROP COLUMN IF EXISTS recipient_contact_person;
ALTER TABLE invoices DROP COLUMN IF EXISTS recipient_org_address;
ALTER TABLE invoices DROP COLUMN IF EXISTS recipient_org_number;
ALTER TABLE invoices DROP COLUMN IF EXISTS recipient_org_name;
ALTER TABLE invoices DROP COLUMN IF EXISTS recipient_kind;
