-- Backfill vipps_import_rows.invoice_id for charges that were assigned to a
-- faktura before the column existed (000103, BRY-340). Those bookings set
-- invoices.payment_id but left invoice_id NULL, so the Vipps backlog tool
-- read their receivable credit as "parked" and could rebook it as revenue
-- (BRY-370).
--
-- The evidence is exact: every manual charge→faktura assignment wrote a
-- 'vipps_charge.assigned_invoice' audit row naming the charge (resource_id),
-- the faktura (details.invoice_id) and the journal entry it posted
-- (details.journal_entry_id). The latest such row per charge in the
-- charge's own club wins. A row is only filled when:
--   * that entry is still the one linked to the charge and still stands
--     (draft or posted; a voided booking no longer settles anything —
--     legacy charge bookings were drafts before BRY-339, so drafts count);
--   * the faktura, in the same club, still reads as paid by the payment
--     this assignment created (linkInvoicePayment writes 'KID-avstemming'
--     for the charge's amount, dated the charge date), not by some later,
--     unrelated payment. The date is compared with a one-day margin on
--     both sides: tx_at is a timestamptz whose ::date depends on the
--     migration session's time zone, and the margin keeps a charge near
--     midnight from being missed without tying the migration to one zone.
-- Anything without such evidence stays NULL and the backlog tool refuses
-- it for a human.

UPDATE vipps_import_rows r
   SET invoice_id = ev.invoice_id
  FROM (
        SELECT DISTINCT ON (a.resource_id)
               a.resource_id,
               (a.details->>'invoice_id')::uuid       AS invoice_id,
               a.details->>'journal_entry_id'         AS journal_entry_id,
               a.club_id
          FROM audit_log a
          JOIN vipps_import_rows vr ON vr.id::text = a.resource_id AND vr.club_id = a.club_id
         WHERE a.action = 'vipps_charge.assigned_invoice'
           AND a.resource = 'vipps_import_row'
           AND a.details->>'invoice_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
         ORDER BY a.resource_id, a.created_at DESC, a.id DESC
       ) ev
  JOIN invoices i ON i.id = ev.invoice_id
  JOIN payments p ON p.id = i.payment_id,
       journal_entries je
 WHERE je.id = r.journal_entry_id
   AND r.id::text = ev.resource_id
   AND r.club_id = ev.club_id
   AND i.club_id = r.club_id
   AND p.club_id = r.club_id
   AND p.description = 'KID-avstemming'
   -- Assignment accepts a charge within 1 kr of the faktura total and
   -- links a payment of the faktura total, so compare with that tolerance.
   AND ABS(p.amount - r.amount) <= 1.00
   AND p.paid_at::date BETWEEN LEAST(r.tx_at::date, COALESCE(r.booking_date, r.tx_at::date)) - 1
                           AND GREATEST(r.tx_at::date, COALESCE(r.booking_date, r.tx_at::date)) + 1
   AND je.club_id = r.club_id
   AND je.status <> 'voided'
   AND r.invoice_id IS NULL
   AND r.journal_entry_id IS NOT NULL
   AND r.journal_entry_id::text = ev.journal_entry_id;
