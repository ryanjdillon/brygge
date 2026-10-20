package accounting

import (
	"context"
	"testing"
	"time"

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

func (f *autoAssignFixture) run(t *testing.T) *AutoAssignResult {
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
	tie := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, CustomerName: "Synthetic Camper"})
	nights := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 120000, CustomerName: "Synthetic Traveller"})
	membersOnly := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 80000, CustomerName: "Synthetic Stranger"})
	// `other` is a mapped category (3900), so a unique price in it is booked
	// like any other category rather than left for review.
	f.priceItem(t, "other", "Synthetic misc fee", 432100, "once", "all")
	other := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 432100, CustomerName: "Synthetic Misc"})

	res := f.run(t)
	targets := map[string]string{}
	for _, a := range res.Assigned {
		targets[a.RowID] = a.Target
	}
	if len(res.Assigned) != 2 || targets[unique] != "3300" || targets[other] != "3900" {
		t.Fatalf("result = %+v, want the unique 1,600 charge booked to 3300 and the other-category charge to 3900", res)
	}
	if got := creditLines(t, f.club, rowEntry(t, f.club, other)); got["3900"][1] != 4321 {
		t.Errorf("other entry lines = %v, want CR 3900 4321", got)
	}
	if rowEntry(t, f.club, tie) != "" {
		t.Errorf("per-day tie was booked without a configured preference")
	}
	if rowEntry(t, f.club, membersOnly) != "" {
		t.Errorf("members-only price booked for a payer who is not a known member")
	}

	// With a tie preference a one-night stay books to motorhome, but a
	// four-night amount is still left for a human: it fits only as a
	// multiple, and the tie preference never settles that.
	f.exec(t, `UPDATE clubs SET vipps_auto_assign_tie_category = 'motorhome' WHERE id = $1`, f.club.ID)
	res = f.run(t)
	if len(res.Assigned) != 1 || res.Assigned[0].RowID != tie || res.Assigned[0].Target != "3210" {
		t.Fatalf("result = %+v, want only the one-night tie booked to 3210", res)
	}
	if got := creditLines(t, f.club, rowEntry(t, f.club, tie)); got["3210"][1] != 300 {
		t.Errorf("tie entry lines = %v, want CR 3210 300", got)
	}
	if rowEntry(t, f.club, nights) != "" {
		t.Errorf("a four-night multiple was booked by the tie preference")
	}
	if d := f.decide(t, 1200); d.Rule != "" || d.Reason != "amount only fits as several nights (4x Synthetic motorhome spot)" {
		t.Errorf("1,200 decision = %+v, want left for a human as several nights", d)
	}
}

func (f *autoAssignFixture) decide(t *testing.T, amount float64) *vippsChargeDecision {
	t.Helper()
	cfg, err := f.svc.vippsAutoAssignConfig(context.Background(), f.club.ID)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	d, err := f.svc.decideVippsCharge(context.Background(), f.club.ID, cfg, "Synthetic Nobody", "", amount, time.Date(f.club.Year, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("decideVippsCharge(%v): %v", amount, err)
	}
	return d
}

// An amount that fits a per-night price only as several nights (2,700 =
// 9 × 300) is left for a human even when that is the only price it fits:
// it is as likely a one-off price no item lists. A single night still books.
func TestAutoAssignLeavesMultiplesManual(t *testing.T) {
	f := newAutoAssignFixture(t)
	f.priceItem(t, "motorhome", "Synthetic motorhome spot", 30000, "day", "all")

	nine := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 270000, CustomerName: "Synthetic Camper"})
	one := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, CustomerName: "Synthetic Camper"})

	res := f.run(t)
	if len(res.Assigned) != 1 || res.Assigned[0].RowID != one || res.Assigned[0].Target != "3210" || res.NeedsReview != 1 {
		t.Fatalf("result = %+v, want only the one-night charge booked and the 2,700 left for review", res)
	}
	if rowEntry(t, f.club, nine) != "" {
		t.Errorf("2,700 was booked as nine nights")
	}
	if d := f.decide(t, 2700); d.Rule != "" || d.Reason != "amount only fits as several nights (9x Synthetic motorhome spot)" {
		t.Errorf("2,700 decision = %+v, want left for a human as several nights", d)
	}
}

// The tie category settles only per-night ties. An amount that is also an
// exact one-off price elsewhere (here a 3,000 season = 10 nights) could be
// that item, so it stays manual even with a preference configured.
func TestAutoAssignTieIgnoresExactOneOffPrices(t *testing.T) {
	f := newAutoAssignFixture(t)
	f.priceItem(t, "guest", "Synthetic guest berth", 30000, "day", "all")
	f.priceItem(t, "motorhome", "Synthetic motorhome spot", 30000, "day", "all")
	f.priceItem(t, "seasonal_rental", "Synthetic summer season", 300000, "season", "all")
	f.exec(t, `UPDATE clubs SET vipps_auto_assign_tie_category = 'motorhome' WHERE id = $1`, f.club.ID)

	season := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 300000, CustomerName: "Synthetic Seasonal"})
	night := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, CustomerName: "Synthetic Camper"})

	res := f.run(t)
	if len(res.Assigned) != 1 || res.Assigned[0].RowID != night || res.Assigned[0].Target != "3210" {
		t.Fatalf("result = %+v, want only the one-night stay booked to 3210", res)
	}
	if rowEntry(t, f.club, season) != "" {
		t.Errorf("3,000 matching an exact season price was absorbed by the tie category")
	}
	if d := f.decide(t, 3000); d.Rule != "" || d.Reason != "amount fits price items in several categories" {
		t.Errorf("3,000 decision = %+v, want several categories", d)
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

// Two members sharing a name are no strong match, so neither member's
// faktura is booked, and the resolver reports no strong pass.
func TestAutoAssignVippsSkipsSharedMemberName(t *testing.T) {
	f := newAutoAssignFixture(t)
	a := f.namedMember(t, "Twinname")
	b := f.namedMember(t, "Twinname")
	f.club.NewInvoice(t, fin.InvoiceOpts{Member: &a, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	f.club.NewInvoice(t, fin.InvoiceOpts{Member: &b, Lines: []fin.Line{{Category: "membership", Cents: 30000}}})
	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Twinname"})

	if _, pass, err := f.svc.resolveCustomerToMemberPass(context.Background(), f.club.ID, "Synthetic Twinname", ""); err != nil || pass <= memberPassFirstLast {
		t.Fatalf("resolver pass = %d (err %v), want no strong match for a shared name", pass, err)
	}
	if res := f.run(t); len(res.Assigned) != 0 || rowEntry(t, f.club, row) != "" {
		t.Errorf("result = %+v, want the charge left for review", res)
	}
}

// The member's only open faktura can't take the charge when it was issued
// after the payment, already has a payment booked against it, or is an
// imported faktura with no GL receivable.
func TestAutoAssignVippsMemberGuards(t *testing.T) {
	f := newAutoAssignFixture(t)
	paid := time.Date(f.club.Year, 6, 1, 0, 0, 0, 0, time.UTC)

	later := f.namedMember(t, "Laterone")
	f.club.NewInvoice(t, fin.InvoiceOpts{Member: &later, IssueDate: paid.AddDate(0, 0, 1), Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	laterRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, Date: paid, CustomerName: "Synthetic Laterone"})

	partly := f.namedMember(t, "Partlyone")
	partInv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &partly, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	first := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 20000, Date: paid, CustomerName: "Synthetic Partlyfirst"})
	if _, err := f.svc.CreateVippsChargeBilag(context.Background(), f.club.ID, first, "3100", "synthetic part payment", f.club.Treasurer.ID); err != nil {
		t.Fatalf("booking the first part: %v", err)
	}
	f.exec(t, `UPDATE vipps_import_rows SET invoice_id = $2 WHERE id = $1`, first, partInv.ID)
	partlyRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, Date: paid, CustomerName: "Synthetic Partlyone"})

	imported := f.namedMember(t, "Importone")
	f.club.NewInvoice(t, fin.InvoiceOpts{Member: &imported, Imported: true, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	importedRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, Date: paid, CustomerName: "Synthetic Importone"})

	f.run(t)
	for name, row := range map[string]string{"issued later": laterRow, "partly paid": partlyRow, "imported": importedRow} {
		if rowEntry(t, f.club, row) != "" {
			t.Errorf("%s faktura: charge was booked", name)
		}
	}
}
