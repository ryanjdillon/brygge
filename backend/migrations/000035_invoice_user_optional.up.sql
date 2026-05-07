-- Single-faktura recipients aren't always club members. An admin
-- might invoice an external organisation with no internal contact, or
-- a private person who isn't (yet) on the membership roll. Drop the
-- NOT NULL constraint so user_id can be omitted; the recipient name
-- and address come from the recipient_* columns in that case, and the
-- faktura simply doesn't surface in any member portal.
ALTER TABLE invoices ALTER COLUMN user_id DROP NOT NULL;
