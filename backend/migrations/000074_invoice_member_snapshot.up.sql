-- Snapshot the member's identity onto each invoice (BRY-205) so historical
-- invoices stay legible — and bokføringsloven-compliant — after the member's
-- PII is anonymized or their user row is removed once retention clears
-- (BRY-206/207). Without this, anonymizing a member rewrites the name/email
-- shown on their past invoices ("Deleted User"), and a hard delete would
-- strip it entirely. Columns are populated at invoice creation; this backfills
-- existing rows from the currently-linked user.
ALTER TABLE invoices
  ADD COLUMN member_name  TEXT,
  ADD COLUMN member_email TEXT;

UPDATE invoices i
   SET member_name  = NULLIF(u.full_name, ''),
       member_email = NULLIF(u.email, '')
  FROM users u
 WHERE u.id = i.user_id
   AND i.member_name IS NULL;
