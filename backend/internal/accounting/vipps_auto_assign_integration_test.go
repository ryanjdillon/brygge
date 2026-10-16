package accounting

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

type autoAssignFixture struct {
	svc  *Service
	club *fin.Club
}

func newAutoAssignFixture(t *testing.T) *autoAssignFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, audit.NewService(pool, zerolog.Nop()), zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	f := &autoAssignFixture{svc: svc, club: club}
	f.exec(t, `UPDATE clubs SET vipps_auto_assign = true WHERE id = $1`, club.ID)
	return f
}

func (f *autoAssignFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.club.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// namedMember seeds a member named "Synthetic <last>"; full_name is
// generated from the parts.
func (f *autoAssignFixture) namedMember(t *testing.T, last string) fin.Member {
	t.Helper()
	m := f.club.NewMember(t, "member")
	f.exec(t, `UPDATE users SET first_name = 'Synthetic', last_name = $2 WHERE id = $1`, m.ID, last)
	return m
}

func (f *autoAssignFixture) priceItem(t *testing.T, category, name string, cents int64, unit, audience string) {
	t.Helper()
	f.exec(t,
		`INSERT INTO price_items (club_id, category, name, amount, unit, is_active, audience)
		 VALUES ($1, $2, $3, $4, $5, true, $6)`,
		f.club.ID, category, name, fin.Kr(cents), unit, audience)
}

func (f *autoAssignFixture) run(t *testing.T) *VippsAutoAssignResult {
	t.Helper()
	res, err := f.svc.AutoAssignVippsCharges(context.Background(), f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("AutoAssignVippsCharges: %v", err)
	}
	return res
}

func (f *autoAssignFixture) invoicePaid(t *testing.T, invoiceID string) bool {
	t.Helper()
	var paid bool
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT payment_id IS NOT NULL FROM invoices WHERE id = $1`, invoiceID).Scan(&paid); err != nil {
		t.Fatalf("invoice: %v", err)
	}
	return paid
}

func TestAutoAssignMemberWithOneOpenFaktura(t *testing.T) {
	f := newAutoAssignFixture(t)
	payer := f.namedMember(t, "Payerone")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &payer, Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payerone"})

	res := f.run(t)
	if len(res.Assigned) != 1 || res.Assigned[0].Rule != autoAssignRuleMemberInvoice || res.NeedsReview != 0 {
		t.Fatalf("result = %+v, want one member_invoice booking", res)
	}
	if !f.invoicePaid(t, inv.ID) {
		t.Errorf("faktura not marked paid")
	}
	if got := creditLines(t, f.club, rowEntry(t, f.club, row)); got["1500"][1] != 450 {
		t.Errorf("entry lines = %v, want CR 1500 450", got)
	}

	var n int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1 AND resource_id = $2`,
		audit.ActionVippsChargeAutoAssigned, row).Scan(&n); err != nil || n != 1 {
		t.Errorf("audit rows = %d (err %v), want 1", n, err)
	}

	// A second run finds nothing left to book.
	if again := f.run(t); len(again.Assigned) != 0 || again.NeedsReview != 0 {
		t.Errorf("rerun = %+v, want a no-op", again)
	}

	// Undo reopens the faktura and puts the charge back in the queue.
	if _, _, err := f.svc.UnassignVippsCharge(context.Background(), f.club.ID, f.club.Treasurer.ID, row); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if f.invoicePaid(t, inv.ID) || rowEntry(t, f.club, row) != "" {
		t.Errorf("unassign left the faktura paid or the charge booked")
	}
}

func TestAutoAssignLeavesAmbiguousMemberChargesManual(t *testing.T) {
	f := newAutoAssignFixture(t)

	two := f.namedMember(t, "Payertwo")
	f.club.NewInvoice(t, fin.InvoiceOpts{Member: &two, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	f.club.NewInvoice(t, fin.InvoiceOpts{Member: &two, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	twoRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payertwo"})

	three := f.namedMember(t, "Payerthree")
	f.club.NewInvoice(t, fin.InvoiceOpts{Member: &three, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	mismatchRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 50000, CustomerName: "Synthetic Payerthree"})

	res := f.run(t)
	if len(res.Assigned) != 0 || res.NeedsReview != 2 {
		t.Fatalf("result = %+v, want nothing booked and 2 for review", res)
	}
	if rowEntry(t, f.club, twoRow) != "" || rowEntry(t, f.club, mismatchRow) != "" {
		t.Errorf("an ambiguous charge was booked")
	}
}

func TestAutoAssignPriceItemAmounts(t *testing.T) {
	f := newAutoAssignFixture(t)
	f.priceItem(t, "service", "Synthetic slipping", 160000, "once", "non_member")
	f.priceItem(t, "guest", "Synthetic guest berth", 30000, "day", "all")
	f.priceItem(t, "motorhome", "Synthetic motorhome spot", 30000, "day", "all")
	f.priceItem(t, "service", "Synthetic member slipping", 80000, "once", "member")

	unique := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 160000, CustomerName: "Synthetic Walk-in"})
	tie := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 120000, CustomerName: "Synthetic Camper"})
	membersOnly := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 80000, CustomerName: "Synthetic Stranger"})

	res := f.run(t)
	if len(res.Assigned) != 1 || res.Assigned[0].RowID != unique || res.Assigned[0].Target != "3300" {
		t.Fatalf("result = %+v, want only the unique 1,600 charge booked to 3300", res)
	}
	if rowEntry(t, f.club, tie) != "" {
		t.Errorf("per-day tie was booked without a configured preference")
	}
	if rowEntry(t, f.club, membersOnly) != "" {
		t.Errorf("members-only price booked for a payer who is not a known member")
	}

	// With a tie preference the four-night stay books to motorhome.
	f.exec(t, `UPDATE clubs SET vipps_auto_assign_tie_category = 'motorhome' WHERE id = $1`, f.club.ID)
	res = f.run(t)
	if len(res.Assigned) != 1 || res.Assigned[0].RowID != tie || res.Assigned[0].Target != "3210" {
		t.Fatalf("result = %+v, want the tie booked to 3210", res)
	}
	if got := creditLines(t, f.club, rowEntry(t, f.club, tie)); got["3210"][1] != 1200 {
		t.Errorf("tie entry lines = %v, want CR 3210 1200", got)
	}
}

func TestAutoAssignIsOptIn(t *testing.T) {
	f := newAutoAssignFixture(t)
	f.exec(t, `UPDATE clubs SET vipps_auto_assign = false WHERE id = $1`, f.club.ID)
	f.priceItem(t, "service", "Synthetic slipping", 160000, "once", "all")
	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 160000})

	res := f.run(t)
	if res.Enabled || len(res.Assigned) != 0 || res.NeedsReview != 1 {
		t.Fatalf("result = %+v, want disabled with one charge for review", res)
	}
	if rowEntry(t, f.club, row) != "" {
		t.Errorf("charge booked while auto-assign is off")
	}
}
