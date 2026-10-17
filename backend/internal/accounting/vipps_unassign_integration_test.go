package accounting

import (
	"context"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// assignedCharge books a Vipps charge against a fresh 450 kr membership
// faktura through the normal per-charge path (posted, faktura settled).
func (f *autoAssignFixture) assignedCharge(t *testing.T) (rowID string, inv fin.Invoice) {
	t.Helper()
	payer := f.namedMember(t, "Undopayer")
	inv = f.club.NewInvoice(t, fin.InvoiceOpts{Member: &payer, Cents: 45000,
		Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	rowID = f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Undopayer"})
	if _, err := f.svc.AssignVippsChargeToInvoice(context.Background(), f.club.ID, rowID, inv.ID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	return rowID, inv
}

func (f *autoAssignFixture) net(t *testing.T, code string) float64 {
	t.Helper()
	var net float64
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(sum(jl.debit) - sum(jl.credit), 0)
		   FROM journal_lines jl JOIN journal_entries je ON je.id = jl.journal_entry_id
		   JOIN accounts a ON a.id = jl.account_id
		  WHERE je.club_id = $1 AND a.code = $2 AND je.status IN ('posted', 'voided')`,
		f.club.ID, code).Scan(&net); err != nil {
		t.Fatalf("net %s: %v", code, err)
	}
	return net
}

func (f *autoAssignFixture) rowEntry(t *testing.T, rowID string) string {
	t.Helper()
	var id *string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT journal_entry_id::text FROM vipps_import_rows WHERE id = $1`, rowID).Scan(&id); err != nil {
		t.Fatalf("row entry: %v", err)
	}
	if id == nil {
		return ""
	}
	return *id
}

// Undo then redo nets to exactly one booking: the first entry and its
// reversal cancel, the re-assignment books once, the faktura ends up paid.
func TestUnassignThenReassignBooksOnce(t *testing.T) {
	f := newAutoAssignFixture(t)
	ctx := context.Background()
	row, inv := f.assignedCharge(t)
	first := f.rowEntry(t, row)

	if _, _, err := f.svc.UnassignVippsCharge(ctx, f.club.ID, f.club.Treasurer.ID, row); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if f.invoicePaid(t, inv.ID) || f.rowEntry(t, row) != "" {
		t.Fatalf("after unassign: faktura still paid or charge still linked")
	}
	if got := f.entryStatus(t, first); got != "voided" {
		t.Errorf("first entry status = %s, want voided", got)
	}
	if got := f.net(t, receivablesAccountCode); got != 0 {
		t.Errorf("1500 net after unassign = %v, want 0", got)
	}

	if _, err := f.svc.AssignVippsChargeToInvoice(ctx, f.club.ID, row, inv.ID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("re-assign: %v", err)
	}
	if !f.invoicePaid(t, inv.ID) {
		t.Errorf("faktura not settled by the re-assignment")
	}
	if got := f.net(t, receivablesAccountCode); got != -450 {
		t.Errorf("1500 net after re-assign = %v, want -450 (one settlement)", got)
	}
}

// A charge booked in a closed period can't be undone in place: nothing is
// voided, unlinked or reopened.
func TestUnassignRefusesClosedPeriod(t *testing.T) {
	f := newAutoAssignFixture(t)
	ctx := context.Background()
	row, inv := f.assignedCharge(t)
	entry := f.rowEntry(t, row)
	f.exec(t, `UPDATE fiscal_periods SET status = 'closed' WHERE id = $1`, f.club.PeriodID)

	if _, _, err := f.svc.UnassignVippsCharge(ctx, f.club.ID, f.club.Treasurer.ID, row); err == nil {
		t.Fatal("unassign in a closed period succeeded")
	}
	if f.rowEntry(t, row) != entry || !f.invoicePaid(t, inv.ID) || f.entryStatus(t, entry) != "posted" {
		t.Errorf("refused unassign changed state: linked=%q paid=%v status=%s",
			f.rowEntry(t, row), f.invoicePaid(t, inv.ID), f.entryStatus(t, entry))
	}
}

// If voiding fails, the charge stays booked and linked: the GL never counts
// an entry for a charge that is back in Tildel.
func TestUnassignVoidFailureChangesNothing(t *testing.T) {
	f := newAutoAssignFixture(t)
	ctx := context.Background()
	row, inv := f.assignedCharge(t)
	entry := f.rowEntry(t, row)
	for _, stmt := range []string{
		`CREATE FUNCTION refuse_void() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN
		   IF NEW.status = 'voided' THEN RAISE EXCEPTION 'synthetic void failure'; END IF;
		   RETURN NEW;
		 END $$`,
		`CREATE TRIGGER refuse_void BEFORE UPDATE ON journal_entries
		 FOR EACH ROW EXECUTE FUNCTION refuse_void()`,
	} {
		f.exec(t, stmt)
	}

	if _, _, err := f.svc.UnassignVippsCharge(ctx, f.club.ID, f.club.Treasurer.ID, row); err == nil {
		t.Fatal("unassign succeeded although the void failed")
	}
	if f.rowEntry(t, row) != entry || !f.invoicePaid(t, inv.ID) || f.entryStatus(t, entry) != "posted" {
		t.Errorf("failed void left a half-undone charge: linked=%q paid=%v status=%s",
			f.rowEntry(t, row), f.invoicePaid(t, inv.ID), f.entryStatus(t, entry))
	}
}

// A retry after the entry was voided but the clearing didn't finish (a
// crash between the two) just completes the clearing.
func TestUnassignRetryAfterVoidCompletes(t *testing.T) {
	f := newAutoAssignFixture(t)
	ctx := context.Background()
	row, inv := f.assignedCharge(t)
	if _, err := f.svc.VoidJournalEntry(ctx, f.club.ID, f.rowEntry(t, row), f.club.Treasurer.ID); err != nil {
		t.Fatalf("void: %v", err)
	}

	if _, _, err := f.svc.UnassignVippsCharge(ctx, f.club.ID, f.club.Treasurer.ID, row); err != nil {
		t.Fatalf("retry unassign: %v", err)
	}
	if f.invoicePaid(t, inv.ID) || f.rowEntry(t, row) != "" {
		t.Errorf("retry didn't finish the clearing")
	}
	if got := f.net(t, receivablesAccountCode); got != 0 {
		t.Errorf("1500 net = %v, want 0 (one entry and one reversal)", got)
	}
}
