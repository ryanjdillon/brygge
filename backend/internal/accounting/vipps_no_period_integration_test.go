package accounting

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// BRY-562: no automatic Vipps pass opens a fiscal year. The club has a 2026
// period and nothing else; the fixture's charges are dated in the year under
// test, and the settlement's deposit lands on that date.

const noPeriodMSN = "000002"

type noPeriodFixture struct {
	svc  *Service
	club *fin.Club
	date time.Time

	priceCharge, memberCharge, orderCharge string
	memberInvoice, orderInvoice            fin.Invoice
	orderRef                               string
	bankRow, payoutRow                     string
}

// newNoPeriodFixture seeds one unbooked charge for each automatic pass dated
// date: a price-item charge and a member-faktura charge (auto-assign), an
// order-linked charge (order reconcile), and a settlement whose charges are
// booked in the club's 2026 period the day before and whose deposit is dated
// date (payout sweep).
func newNoPeriodFixture(t *testing.T, date time.Time) *noPeriodFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, audit.NewService(pool, zerolog.Nop()), zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	f := &noPeriodFixture{svc: svc, club: club, date: date}
	f.exec(t, `UPDATE clubs SET vipps_auto_assign = true, vipps_per_charge_enabled = true WHERE id = $1`, club.ID)
	f.exec(t,
		`INSERT INTO price_items (club_id, category, name, amount, unit, is_active, audience)
		 VALUES ($1, 'service', 'Synthetic slipping', 300, 'once', true, 'all')`, club.ID)

	payer := club.NewMember(t, "member")
	f.exec(t, `UPDATE users SET first_name = 'Synthetic', last_name = 'Payerone' WHERE id = $1`, payer.ID)
	f.memberInvoice = club.NewInvoice(t, fin.InvoiceOpts{Member: &payer, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	f.memberCharge = club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, Date: date, CustomerName: "Synthetic Payerone"})

	f.priceCharge = club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, Date: date, CustomerName: "Synthetic Walkup"})

	f.orderInvoice = club.NewInvoice(t, fin.InvoiceOpts{Lines: []fin.Line{{Category: "membership", Cents: 60000}}})
	order := club.NewOrderForInvoice(t, f.orderInvoice)
	f.orderRef = order.VippsReference
	f.orderCharge = club.NewVippsRow(t, fin.VippsRowOpts{Cents: 60000, Date: date, OrderID: order.VippsReference})

	// The settlement: booked in the year before the deposit's date.
	booking := date.AddDate(0, 0, -1)
	charge := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 20000, Date: booking, MSN: noPeriodMSN})
	club.NewVippsRow(t, fin.VippsRowOpts{RowType: "fee", Cents: -1000, Date: booking, MSN: noPeriodMSN})
	f.payoutRow = club.NewVippsRow(t, fin.VippsRowOpts{RowType: "payout", Cents: -19000, Date: booking, MSN: noPeriodMSN, Settlement: "4000001"})
	f.bankRow = club.NewBankRow(t, club.NewBankImport(t), fin.BankRowOpts{
		Date: date, Cents: 19000, Description: "Utb. 4000001 Vippsnr " + noPeriodMSN,
	})
	if _, err := svc.CreateVippsChargeBilag(ctx, club.ID, charge, "3200", "", club.Treasurer.ID); err != nil {
		t.Fatalf("booking the settlement's charge: %v", err)
	}
	return f
}

func (f *noPeriodFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.club.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func (f *noPeriodFixture) count(t *testing.T, sql string) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(), sql, f.club.ID).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

func (f *noPeriodFixture) periods(t *testing.T) int {
	return f.count(t, `SELECT count(*) FROM fiscal_periods WHERE club_id = $1`)
}

func (f *noPeriodFixture) entries(t *testing.T) int {
	return f.count(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1`)
}

// entry returns the date and fiscal period of a journal entry.
func (f *noPeriodFixture) entry(t *testing.T, entryID string) (date, periodID string) {
	t.Helper()
	if entryID == "" {
		t.Fatal("not booked: no journal entry")
	}
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT entry_date::text, fiscal_period_id::text FROM journal_entries WHERE id = $1`, entryID,
	).Scan(&date, &periodID); err != nil {
		t.Fatalf("read entry %s: %v", entryID, err)
	}
	return date, periodID
}

func (f *noPeriodFixture) bankEntryID(t *testing.T) string {
	t.Helper()
	var id *string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT journal_entry_id::text FROM bank_import_rows WHERE id = $1`, f.bankRow).Scan(&id); err != nil {
		t.Fatalf("read bank row: %v", err)
	}
	if id == nil {
		return ""
	}
	return *id
}

func (f *noPeriodFixture) invoicePaid(t *testing.T, invoiceID string) bool {
	t.Helper()
	var paid bool
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT payment_id IS NOT NULL FROM invoices WHERE id = $1`, invoiceID).Scan(&paid); err != nil {
		t.Fatalf("invoice: %v", err)
	}
	return paid
}

type passResults struct {
	assign *AutoAssignResult
	orders *VippsOrderReconcileResult
	sweep  *VippsPayoutSweepResult
}

// runPasses runs the three automatic Vipps passes, in the order a sync does.
func (f *noPeriodFixture) runPasses(t *testing.T) passResults {
	t.Helper()
	ctx := context.Background()
	var r passResults
	var err error
	if r.orders, err = f.svc.AutoReconcileOrderCharges(ctx, f.club.ID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("AutoReconcileOrderCharges: %v", err)
	}
	if r.assign, err = f.svc.AutoAssignVippsCharges(ctx, f.club.ID, f.club.Treasurer.ID, VippsAutoAssignOpts{}); err != nil {
		t.Fatalf("AutoAssignVippsCharges: %v", err)
	}
	if r.sweep, err = f.svc.ClearReadyVippsPayouts(ctx, f.club.ID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("ClearReadyVippsPayouts: %v", err)
	}
	return r
}

func TestAutomaticVippsPassesNeverCreateFiscalPeriod(t *testing.T) {
	ctx := context.Background()
	newYear := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	f := newNoPeriodFixture(t, newYear)
	periodsBefore, entriesBefore := f.periods(t), f.entries(t)
	if periodsBefore != 1 {
		t.Fatalf("fixture has %d fiscal periods, want only 2026", periodsBefore)
	}

	const wantReason = "ingen regnskapsår for 2027"
	r := f.runPasses(t)

	// Every pass skips with the no-fiscal-year reason.
	if r.assign.Source != AutoAssignSourceVipps || len(r.assign.Assigned) != 0 || len(r.assign.SkippedDetails) != 2 || r.assign.NeedsReview != 2 {
		t.Fatalf("auto-assign = %+v, want the price-item and the member-faktura charge skipped", r.assign)
	}
	for _, s := range r.assign.SkippedDetails {
		if s.Reason != wantReason {
			t.Errorf("auto-assign skipped %q with reason %q, want %q", s.Ref, s.Reason, wantReason)
		}
	}
	if r.orders.Booked != 0 || len(r.orders.SkippedDetails) != 1 || r.orders.SkippedDetails[0].Reason != wantReason {
		t.Errorf("order reconcile = %+v, want the order-linked charge skipped with %q", r.orders, wantReason)
	}
	if len(r.sweep.Cleared) != 0 || len(r.sweep.Failed) != 0 || len(r.sweep.Skipped) != 1 ||
		!strings.Contains(r.sweep.Skipped[0].Reason, wantReason) {
		t.Errorf("payout sweep = %+v, want the settlement skipped with %q", r.sweep, wantReason)
	}

	// Nothing was created or posted.
	if got := f.periods(t); got != periodsBefore {
		t.Errorf("fiscal_periods rows = %d, want %d: an automatic pass opened a year", got, periodsBefore)
	}
	if got := f.entries(t); got != entriesBefore {
		t.Errorf("journal entries = %d, want %d: an automatic pass posted", got, entriesBefore)
	}
	for name, id := range map[string]string{"price": f.priceCharge, "member": f.memberCharge, "order": f.orderCharge} {
		if e := rowEntry(t, f.club, id); e != "" {
			t.Errorf("%s charge booked on %s", name, e)
		}
	}
	if f.bankEntryID(t) != "" || f.invoicePaid(t, f.memberInvoice.ID) || f.invoicePaid(t, f.orderInvoice.ID) {
		t.Errorf("the deposit was cleared or a faktura marked paid")
	}

	// The treasurer opens the year; the first pass after that books each
	// payment in its own year, dated on its own day.
	p, err := f.svc.CreatePeriod(ctx, f.club.ID, 2027)
	if err != nil {
		t.Fatalf("CreatePeriod: %v", err)
	}
	r = f.runPasses(t)
	if len(r.assign.Assigned) != 2 || len(r.assign.SkippedDetails) != 0 || r.orders.Booked != 1 || len(r.sweep.Cleared) != 1 {
		t.Fatalf("after the year exists: assign %+v, orders %+v, sweep %+v; want everything booked", r.assign, r.orders, r.sweep)
	}
	for name, entryID := range map[string]string{
		"price":  rowEntry(t, f.club, f.priceCharge),
		"member": rowEntry(t, f.club, f.memberCharge),
		"order":  rowEntry(t, f.club, f.orderCharge),
		"bank":   f.bankEntryID(t),
	} {
		date, periodID := f.entry(t, entryID)
		if date != "2027-01-01" || periodID != p.ID {
			t.Errorf("%s entry dated %s in period %s, want 2027-01-01 in the 2027 period %s", name, date, periodID, p.ID)
		}
	}
	if !f.invoicePaid(t, f.memberInvoice.ID) || !f.invoicePaid(t, f.orderInvoice.ID) {
		t.Errorf("fakturas not marked paid once booked")
	}
	if got := f.periods(t); got != 2 {
		t.Errorf("fiscal_periods rows = %d, want 2026 and 2027", got)
	}
}

// A period that is not open gets its own reason, not the generic "cannot post
// to a closed or locked period" of the entry insert.
func TestAutomaticVippsPassesRefuseAPeriodThatIsNotOpen(t *testing.T) {
	cases := []struct {
		status        string
		wantCharge    string
		wantSweepText string
	}{
		{"locked", "regnskapsperioden for 2026 er låst", "regnskapsperioden er låst"},
		{"closed", "regnskapsperioden for 2026 er lukket", "regnskapsperioden er lukket"},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			f := newNoPeriodFixture(t, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
			f.exec(t, `UPDATE fiscal_periods SET status = $2::fiscal_period_status WHERE id = $1`, f.club.PeriodID, tc.status)
			entriesBefore := f.entries(t)

			r := f.runPasses(t)

			if len(r.assign.Assigned) != 0 || len(r.assign.SkippedDetails) != 2 {
				t.Fatalf("auto-assign = %+v, want both charges skipped", r.assign)
			}
			for _, s := range r.assign.SkippedDetails {
				if s.Reason != tc.wantCharge {
					t.Errorf("auto-assign skipped %q with reason %q, want %q", s.Ref, s.Reason, tc.wantCharge)
				}
			}
			if r.orders.Booked != 0 || len(r.orders.SkippedDetails) != 1 || r.orders.SkippedDetails[0].Reason != tc.wantCharge {
				t.Errorf("order reconcile = %+v, want the charge skipped with %q", r.orders, tc.wantCharge)
			}
			if len(r.sweep.Cleared) != 0 || len(r.sweep.Failed) != 1 || r.sweep.Failed[0].Reason != tc.wantSweepText {
				t.Errorf("payout sweep = %+v, want the settlement failed with %q", r.sweep, tc.wantSweepText)
			}
			if got := f.entries(t); got != entriesBefore {
				t.Errorf("journal entries = %d, want %d", got, entriesBefore)
			}
		})
	}
}

// A person's booking in Tildel keeps today's behaviour: a charge dated in a
// year with no fiscal period opens that year as a calendar-year period and is
// dated on its own day. This is not changed by BRY-562.
func TestManualVippsBookingStillCreatesTheFiscalPeriod(t *testing.T) {
	newYear := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	assertCreated := func(t *testing.T, f *noPeriodFixture, entryID string) {
		t.Helper()
		date, periodID := f.entry(t, entryID)
		var year int
		var status string
		if err := f.club.Pool.QueryRow(context.Background(),
			`SELECT year, status FROM fiscal_periods WHERE id = $1`, periodID).Scan(&year, &status); err != nil {
			t.Fatalf("read period: %v", err)
		}
		if date != "2027-01-01" || year != 2027 || status != "open" {
			t.Errorf("entry dated %s in the %d period (%s), want 2027-01-01 in a new open 2027 period", date, year, status)
		}
		if got := f.periods(t); got != 2 {
			t.Errorf("fiscal_periods rows = %d, want 2026 and the created 2027", got)
		}
	}

	t.Run("Lag bilag", func(t *testing.T) {
		f := newNoPeriodFixture(t, newYear)
		res, err := f.svc.CreateVippsChargeBilag(context.Background(), f.club.ID, f.priceCharge, "3200", "", f.club.Treasurer.ID)
		if err != nil {
			t.Fatalf("CreateVippsChargeBilag: %v", err)
		}
		assertCreated(t, f, res.EntryID)
	})
	t.Run("Søk faktura", func(t *testing.T) {
		f := newNoPeriodFixture(t, newYear)
		res, err := f.svc.AssignVippsChargeToInvoice(context.Background(), f.club.ID, f.memberCharge, f.memberInvoice.ID, f.club.Treasurer.ID)
		if err != nil {
			t.Fatalf("AssignVippsChargeToInvoice: %v", err)
		}
		assertCreated(t, f, res.EntryID)
	})
	t.Run("Avstem Vipps-utbetaling", func(t *testing.T) {
		f := newNoPeriodFixture(t, newYear)
		res, err := f.svc.ClearVippsPayout(context.Background(), f.club.ID, f.bankRow, f.club.Treasurer.ID)
		if err != nil {
			t.Fatalf("ClearVippsPayout: %v", err)
		}
		assertCreated(t, f, res.BankEntryID)
	})
}
