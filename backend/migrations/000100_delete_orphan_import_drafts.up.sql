-- Remove the orphan draft entries left by import-time KID auto-match before
-- BRY-324. The importer passed the club id as posted_by, the post failed on
-- the users FK, and the draft was abandoned while its bank row stayed
-- unmatched; a later bank sync then booked the same row properly. Each such
-- draft therefore duplicates a posted booking. Posting one would book the
-- payment twice.
--
-- Deletes only drafts created by the importer itself (source_table
-- 'bank_import', source_id = the import) that are linked to no row and whose
-- import has a row with the same date and amount already linked to a posted
-- entry. Drafts never count in reports, so the GL is unchanged. Lines go with
-- the entry via ON DELETE CASCADE.

DELETE FROM journal_entries je
 WHERE je.source = 'bank_import'
   AND je.source_table = 'bank_import'
   AND je.status = 'draft'
   AND NOT EXISTS (SELECT 1 FROM bank_import_rows r WHERE r.journal_entry_id = je.id)
   AND EXISTS (
       SELECT 1
         FROM bank_import_rows b
         JOIN journal_entries p ON p.id = b.journal_entry_id AND p.status = 'posted'
        WHERE b.bank_import_id::text = je.source_id
          AND b.row_date = je.entry_date
          AND b.amount = (SELECT max(jl.debit) FROM journal_lines jl WHERE jl.journal_entry_id = je.id)
   );
