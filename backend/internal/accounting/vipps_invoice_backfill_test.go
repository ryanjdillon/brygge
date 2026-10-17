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

	// Exact evidence: filled.
	good := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000})
	goodInv := paidInvoice()
	audited(good, goodInv.ID, f.draftCharge(t, good, receivablesAccountCode, 45000))

	// The audit names an entry that is no longer the row's: left alone.
	stale := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000})
	staleInv := paidInvoice()
	f.draftCharge(t, stale, receivablesAccountCode, 45000)
	audited(stale, staleInv.ID, "00000000-0000-0000-0000-000000000001")

	// The faktura is no longer paid (payment unlinked since): left alone.
	reopened := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000})
	reopenedInv := paidInvoice()
	audited(reopened, reopenedInv.ID, f.draftCharge(t, reopened, receivablesAccountCode, 45000))
	f.exec(t, `UPDATE invoices SET payment_id = NULL WHERE id = $1`, reopenedInv.ID)

	// No audit evidence at all: left alone.
	bare := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000})
	f.draftCharge(t, bare, receivablesAccountCode, 45000)

	for run := 0; run < 2; run++ {
		if _, err := f.club.Pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	if got := invoiceOf(good); got != goodInv.ID {
		t.Errorf("exact evidence: invoice_id = %q, want %q", got, goodInv.ID)
	}
	for name, row := range map[string]string{"stale entry": stale, "reopened faktura": reopened, "no audit": bare} {
		if got := invoiceOf(row); got != "" {
			t.Errorf("%s: invoice_id = %q, want it left NULL", name, got)
		}
	}

	// The backfilled draft now posts as a faktura settlement; the rest are refused.
	res := f.backlog(t, false)
	if res.Posted != 1 || len(res.Refused) != 3 {
		t.Errorf("backlog preview after backfill = %+v, want 1 postable and 3 refused", res)
	}
}
