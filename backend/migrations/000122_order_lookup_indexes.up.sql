-- Indexes for two order-linked lookups that had none (BRY-473, BRY-459).
--
-- Plain CREATE INDEX, not CONCURRENTLY: golang-migrate runs the file in one
-- transaction, where CONCURRENTLY is refused. The tables are small, so the
-- brief SHARE lock is fine.
--
-- Each partial predicate repeats the query's own condition (the planner only
-- uses a partial index when the query's WHERE implies the predicate), so a
-- change to those queries must change the index with it:
--   * orders: recentPaymentsQuery (handlers/harbor_payments.go)
--   * stripe_balance_transactions: stripeChargeSettlesSQL
--     (accounting/stripe_manual.go) and the bsi/bst clauses of
--     invoiceBookedRowSQL (accounting/auto_assign_member.go), which add the
--     review_reason condition and the invoice/order join shape
--   * vipps_import_rows: invoiceBookedRowSQL
--     (accounting/auto_assign_member.go)

-- The payment overview lists a club's paid or refunded orders newest first.
-- NULLS LAST is spelled out because DESC defaults to NULLS FIRST in Postgres;
-- without it the index order does not match the query's ORDER BY.
CREATE INDEX IF NOT EXISTS idx_orders_club_paid
    ON orders (club_id, paid_at DESC NULLS LAST)
    WHERE status IN ('paid'::order_status, 'refunded'::order_status);

-- "Is this faktura already booked" reads a Stripe charge by the faktura it
-- was assigned to, or through its order (order_sources). Only a charge that
-- still settles counts, and an order-booked one carries no review_reason.
CREATE INDEX IF NOT EXISTS idx_stripe_bt_settling_invoice
    ON stripe_balance_transactions (club_id, invoice_id)
    WHERE reporting_category = 'charge' AND journal_entry_id IS NOT NULL
      AND recollected_at IS NULL AND invoice_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_stripe_bt_settling_order
    ON stripe_balance_transactions (order_id)
    WHERE reporting_category = 'charge' AND journal_entry_id IS NOT NULL
      AND recollected_at IS NULL AND review_reason IS NULL AND order_id IS NOT NULL;

-- The same check reads a booked Vipps charge by its faktura or by the
-- merchant reference of the order that paid it.
CREATE INDEX IF NOT EXISTS idx_vipps_import_rows_booked_invoice
    ON vipps_import_rows (club_id, invoice_id)
    WHERE journal_entry_id IS NOT NULL AND invoice_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_vipps_import_rows_booked_order
    ON vipps_import_rows (club_id, order_id)
    WHERE journal_entry_id IS NOT NULL;
