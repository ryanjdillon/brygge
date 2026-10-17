-- Backfill vipps_import_rows.invoice_id for charges that were assigned to a
-- faktura before the column existed (000103, BRY-340). Those bookings set
-- invoices.payment_id but left invoice_id NULL, so the Vipps backlog tool
-- read their receivable credit as "parked" and could rebook it as revenue
-- (BRY-370).
--
-- The evidence is exact: every manual charge→faktura assignment wrote a
-- 'vipps_charge.assigned_invoice' audit row naming the charge (resource_id),
-- the faktura (details.invoice_id) and the journal entry it posted
-- (details.journal_entry_id). A row is only filled when that entry is still
-- the one linked to the charge and the faktura, in the same club, still
-- reads as paid. Anything without such evidence stays NULL and the backlog
-- tool refuses it for a human.

UPDATE vipps_import_rows r
   SET invoice_id = ev.invoice_id
  FROM (
        SELECT DISTINCT ON (a.resource_id)
               a.resource_id,
               (a.details->>'invoice_id')::uuid       AS invoice_id,
               a.details->>'journal_entry_id'         AS journal_entry_id,
               a.club_id
          FROM audit_log a
         WHERE a.action = 'vipps_charge.assigned_invoice'
           AND a.resource = 'vipps_import_row'
           AND a.details->>'invoice_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
         ORDER BY a.resource_id, a.created_at DESC
       ) ev
  JOIN invoices i ON i.id = ev.invoice_id
 WHERE r.id::text = ev.resource_id
   AND r.club_id = ev.club_id
   AND i.club_id = r.club_id
   AND i.payment_id IS NOT NULL
   AND r.invoice_id IS NULL
   AND r.journal_entry_id IS NOT NULL
   AND r.journal_entry_id::text = ev.journal_entry_id;
