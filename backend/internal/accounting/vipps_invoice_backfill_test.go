package accounting

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// Migration 000106 fills vipps_import_rows.invoice_id only from an exact
// audit trail: the assign audit row naming the charge, the faktura and the
// entry still linked to the charge, with the faktura still paid. Re-running
// the statement (the migration is plain SQL) must change nothing more.
func TestVippsInvoiceBackfillUsesOnlyExactAuditEvidence(t *testing.T) {
	f := newAutoAssignFixture(t)
	ctx := context.Background()
	sql, err := os.ReadFile(filepath.Join(testutil.MigrationsDir(), "000106_vipps_row_invoice_backfill.up.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}

	audited := func(rowID, invoiceID, entryID string) {
		f.exec(t,
			`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
			 VALUES ($1, $2, 'vipps_charge.assigned_invoice', 'vipps_import_row', $3,
			         jsonb_build_object('invoice_id', $4::text, 'journal_entry_id', $5::text))`,
			f.club.ID, f.club.Treasurer.ID, rowID, invoiceID, entryID)
	}
	invoiceOf := func(rowID string) string {
		var id *string
		if err := f.club.Pool.QueryRow(ctx, `SELECT invoice_id::text FROM vipps_import_rows WHERE id = $1`, rowID).Scan(&id); err != nil {
			t.Fatalf("read invoice_id: %v", err)
		}
		if id == nil {
			return ""
		}
		return *id
	}
	paidInvoice := func() fin.Invoice {
		m := f.club.NewMember(t, "member")
		return f.club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Cents: 45000, Paid: true,
			Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	}
	// paidByCharge makes the faktura's payment the one AssignVippsChargeToInvoice
	// would have written for this charge (linkInvoicePayment: 'KID-avstemming',
	// dated the charge date).
	paidByCharge := func(inv fin.Invoice, rowID string) {
		f.exec(t,
			`UPDATE payments SET description = 'KID-avstemming',
			        paid_at = (SELECT COALESCE(tx_at, booking_date::timestamptz) FROM vipps_import_rows WHERE id = $2)
			  WHERE id = (SELECT payment_id FROM invoices WHERE id = $1)`,
			inv.ID, rowID)
	}

	// Exact evidence: filled.
	good := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000})
	goodInv := paidInvoice()
	paidByCharge(goodInv, good)
	audited(good, goodInv.ID, f.draftCharge(t, good, receivablesAccountCode, 45000))

	// Several audit rows for one charge: the latest names the faktura that
	// is actually paid by it, so it wins over an earlier, superseded one.
	retried := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000})
	retriedEntry := f.draftCharge(t, retried, receivablesAccountCode, 45000)
	audited(retried, paidInvoice().ID, retriedEntry)
	retriedInv := paidInvoice()
	paidByCharge(retriedInv, retried)
	f.exec(t, `INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details, created_at)
		VALUES ($1, $2, 'vipps_charge.assigned_invoice', 'vipps_import_row', $3,
		        jsonb_build_object('invoice_id', $4::text, 'journal_entry_id', $5::text), now() + interval '1 minute')`,
		f.club.ID, f.club.Treasurer.ID, retried, retriedInv.ID, retriedEntry)

	// The audit names an entry that is no longer the row's: left alone.
	stale := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000})
	staleInv := paidInvoice()
	paidByCharge(staleInv, stale)
	f.draftCharge(t, stale, receivablesAccountCode, 45000)
	audited(stale, staleInv.ID, "00000000-0000-0000-0000-000000000001")

	// The faktura is no longer paid (payment unlinked since): left alone.
	reopened := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000})
	reopenedInv := paidInvoice()
	paidByCharge(reopenedInv, reopened)
	audited(reopened, reopenedInv.ID, f.draftCharge(t, reopened, receivablesAccountCode, 45000))
	f.exec(t, `UPDATE invoices SET payment_id = NULL WHERE id = $1`, reopenedInv.ID)

	// The faktura is paid, but by some other payment (a bank KID payment,
	// not this charge's 'KID-avstemming' on the charge date): left alone.
	otherPayment := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000})
	otherPaymentInv := paidInvoice()
	audited(otherPayment, otherPaymentInv.ID, f.draftCharge(t, otherPayment, receivablesAccountCode, 45000))

	// The booking was voided but the row still points at it: left alone.
	voided := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000})
	voidedInv := paidInvoice()
	paidByCharge(voidedInv, voided)
	voidedEntry := f.draftCharge(t, voided, receivablesAccountCode, 45000)
	audited(voided, voidedInv.ID, voidedEntry)
	f.exec(t, `UPDATE journal_entries SET status = 'voided' WHERE id = $1`, voidedEntry)

	// The only audit row comes from another club: left alone.
	foreign := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000})
	foreignInv := paidInvoice()
	paidByCharge(foreignInv, foreign)
	foreignEntry := f.draftCharge(t, foreign, receivablesAccountCode, 45000)
	other := fin.NewClub(t, f.club.Pool, fin.WithKontoplan(f.svc.SeedKontoplan))
	f.exec(t,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
		 VALUES ($1, $2, 'vipps_charge.assigned_invoice', 'vipps_import_row', $3,
		         jsonb_build_object('invoice_id', $4::text, 'journal_entry_id', $5::text))`,
		other.ID, other.Treasurer.ID, foreign, foreignInv.ID, foreignEntry)

	// The payment's date falls on the neighbouring calendar day (a charge
	// near midnight read in another time zone): still exact evidence.
	nearMidnight := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000})
	nearMidnightInv := paidInvoice()
	paidByCharge(nearMidnightInv, nearMidnight)
	f.exec(t, `UPDATE payments SET paid_at = paid_at + interval '1 day'
	            WHERE id = (SELECT payment_id FROM invoices WHERE id = $1)`, nearMidnightInv.ID)
	audited(nearMidnight, nearMidnightInv.ID, f.draftCharge(t, nearMidnight, receivablesAccountCode, 45000))

	// Two days off, or a different amount: not this charge's payment.
	farOff := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000})
	farOffInv := paidInvoice()
	paidByCharge(farOffInv, farOff)
	f.exec(t, `UPDATE payments SET paid_at = paid_at + interval '2 days'
	            WHERE id = (SELECT payment_id FROM invoices WHERE id = $1)`, farOffInv.ID)
	audited(farOff, farOffInv.ID, f.draftCharge(t, farOff, receivablesAccountCode, 45000))

	otherAmount := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000})
	otherAmountInv := paidInvoice()
	paidByCharge(otherAmountInv, otherAmount)
	f.exec(t, `UPDATE payments SET amount = amount + 1
	            WHERE id = (SELECT payment_id FROM invoices WHERE id = $1)`, otherAmountInv.ID)
	audited(otherAmount, otherAmountInv.ID, f.draftCharge(t, otherAmount, receivablesAccountCode, 45000))

	// No audit evidence at all: left alone.
	bare := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000})
	f.draftCharge(t, bare, receivablesAccountCode, 45000)

	for run := 0; run < 2; run++ {
		if _, err := f.club.Pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	for name, c := range map[string]struct{ row, want string }{
		"exact evidence":    {good, goodInv.ID},
		"latest audit wins": {retried, retriedInv.ID},
		"near midnight":     {nearMidnight, nearMidnightInv.ID},
	} {
		if got := invoiceOf(c.row); got != c.want {
			t.Errorf("%s: invoice_id = %q, want %q", name, got, c.want)
		}
	}
	for name, row := range map[string]string{
		"stale entry": stale, "reopened faktura": reopened, "other payment": otherPayment,
		"voided entry": voided, "foreign audit": foreign, "no audit": bare,
		"two days off": farOff, "other amount": otherAmount,
	} {
		if got := invoiceOf(row); got != "" {
			t.Errorf("%s: invoice_id = %q, want it left NULL", name, got)
		}
	}

	// The backfilled draft now posts as a faktura settlement; the rest are refused.
	// The voided booking is no longer a draft, so the backlog sees 10 drafts:
	// the three backfilled settle their fakturas, the other seven are refused.
	res := f.backlog(t, false)
	if res.Posted != 3 || len(res.Refused) != 7 {
		t.Errorf("backlog preview after backfill = %+v, want 3 postable and 7 refused", res)
	}
}
