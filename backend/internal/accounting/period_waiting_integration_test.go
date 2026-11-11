package accounting

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// BRY-575: payments waiting for a missing, closed or locked fiscal year. The
// club has a 2026 period (fin.NewClub); every row is synthetic.

type waitFixture struct {
	svc  *Service
	club *fin.Club
	imp  string
}

func newWaitFixture(t *testing.T) *waitFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, audit.NewService(pool, zerolog.Nop()), zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	f := &waitFixture{svc: svc, club: club, imp: club.NewBankImport(t)}
	f.exec(t, `UPDATE clubs SET vipps_auto_assign = true, vipps_per_charge_enabled = true WHERE id = $1`, club.ID)
	return f
}

func (f *waitFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.club.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// period inserts a calendar-year period for the club with the given status.
func (f *waitFixture) period(t *testing.T, year int, status string, reference bool) string {
	t.Helper()
	var id string
	if err := f.club.Pool.QueryRow(context.Background(),
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status, closed_at, reference_only)
		 VALUES ($1, $2, make_date($2, 1, 1), make_date($2, 12, 31), $3::fiscal_period_status,
		         CASE WHEN $3 = 'open' THEN NULL ELSE now() END, $4)
		 RETURNING id`,
		f.club.ID, year, status, reference,
	).Scan(&id); err != nil {
		t.Fatalf("period %d: %v", year, err)
	}
	return id
}

func (f *waitFixture) kidRow(t *testing.T, date time.Time) string {
	t.Helper()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	return f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: date, Cents: 45000, KID: inv.KID})
}

// stripeRow stores a Stripe balance transaction as ingest leaves it.
func (f *waitFixture) stripeRow(t *testing.T, category, status string, created time.Time, withOrder bool) {
	t.Helper()
	var orderID *string
	if withOrder {
		inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 30000})
		o := f.club.NewOrderForInvoice(t, inv)
		orderID = &o.ID
	}
	var reason *string
	if status == "needs_review" {
		r := StripeReviewReceivableNotOpen
		reason = &r
	}
	f.exec(t,
		`INSERT INTO stripe_balance_transactions
		     (club_id, balance_transaction_id, reporting_category, stripe_type, amount_ore, fee_ore, net_ore,
		      currency, created_at, order_id, status, review_reason)
		 VALUES ($1, $2, $3, $3, 30000, 0, 30000, 'nok', $4, $5, $6, $7)`,
		f.club.ID, "txn_"+testutil.RandomHex(6), category, created, orderID, status, reason)
}

func (f *waitFixture) waiting(t *testing.T) []WaitingFiscalYear {
	t.Helper()
	w, err := f.svc.WaitingFiscalYears(context.Background(), f.club.ID)
	if err != nil {
		t.Fatalf("WaitingFiscalYears: %v", err)
	}
	return w
}

func wday(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func assertWaiting(t *testing.T, got []WaitingFiscalYear, want ...WaitingFiscalYear) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("waiting = %+v, want %+v", got, want)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Year != w.Year || g.State != w.State || g.Payments != w.Payments {
			t.Errorf("waiting[%d] = {%d %s %d}, want {%d %s %d}", i, g.Year, g.State, g.Payments, w.Year, w.State, w.Payments)
		}
		if (w.PeriodID == nil) != (g.PeriodID == nil) || (w.PeriodID != nil && *w.PeriodID != *g.PeriodID) {
			t.Errorf("waiting[%d] period = %v, want %v", i, g.PeriodID, w.PeriodID)
		}
		if !w.Oldest.IsZero() && !g.Oldest.Equal(w.Oldest) {
			t.Errorf("waiting[%d] oldest = %s, want %s", i, g.Oldest, w.Oldest)
		}
	}
}

func TestWaitingFiscalYearsMissingYear(t *testing.T) {
	ctx := context.Background()
	f := newWaitFixture(t)
	jan2 := wday(2027, 1, 2)
	f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, Date: jan2})
	f.kidRow(t, jan2)
	// A Vipps settlement deposit waits with them but is not a payment.
	f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: wday(2027, 1, 5), Cents: 19000, Description: "Utb. 4000001 Vippsnr 000001"})
	// Dismissed rows, a person's decline, a KID-less deposit, an outgoing
	// row and a non-charge Vipps row wait for nobody.
	dismissed := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 10000, Date: jan2})
	f.exec(t, `UPDATE vipps_import_rows SET dismissed_at = now() WHERE id = $1`, dismissed)
	dismissedBank := f.kidRow(t, jan2)
	f.exec(t, `UPDATE bank_import_rows SET dismissed_at = now(), dismissed_by = $2, dismissed_reason = 'duplicate' WHERE id = $1`, dismissedBank, f.club.Treasurer.ID)
	declined := f.kidRow(t, wday(2027, 1, 3))
	f.exec(t, `UPDATE bank_import_rows SET auto_declined_at = now() WHERE id = $1`, declined)
	f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: jan2, Cents: 50000, Description: "Synthetic gift"})
	f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: jan2, Cents: -50000, KID: "000000000001"})
	f.club.NewVippsRow(t, fin.VippsRowOpts{RowType: "fee", Cents: -500, Date: jan2})
	// A payment in the open year waits for nothing.
	f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, Date: wday(2026, 6, 1)})

	assertWaiting(t, f.waiting(t), WaitingFiscalYear{Year: 2027, State: WaitingMissing, Payments: 2, Oldest: jan2})

	if _, err := f.svc.CreatePeriod(ctx, f.club.ID, 2027); err != nil {
		t.Fatalf("CreatePeriod: %v", err)
	}
	assertWaiting(t, f.waiting(t))
}

func TestWaitingFiscalYearsClosedAndLockedPeriods(t *testing.T) {
	f := newWaitFixture(t)
	closed := f.period(t, 2025, "closed", false)
	locked := f.period(t, 2024, "locked", false)
	f.kidRow(t, wday(2025, 6, 1))
	f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, Date: wday(2025, 7, 1)})
	f.kidRow(t, wday(2024, 3, 1))

	got := f.waiting(t)
	assertWaiting(t, got,
		WaitingFiscalYear{Year: 2024, State: WaitingLocked, PeriodID: &locked, Payments: 1},
		WaitingFiscalYear{Year: 2025, State: WaitingClosed, PeriodID: &closed, Payments: 2, Oldest: wday(2025, 6, 1)},
	)
	if got[1].ClosedAt == nil {
		t.Errorf("closed period carries no closed_at")
	}
	// The closed period's ref names the period and its closing, so a later
	// close is a new notice.
	wantRef := fmt.Sprintf("fiscal_year_waiting:%s:2025:closed:%s:%d", f.club.ID, closed, got[1].ClosedAt.Unix())
	if ref := got[1].Ref(f.club.ID); ref != wantRef {
		t.Errorf("closed ref = %q, want %q", ref, wantRef)
	}
	// Reopening the year ends the condition.
	if err := f.svc.ReopenPeriod(context.Background(), f.club.ID, closed); err != nil {
		t.Fatalf("ReopenPeriod: %v", err)
	}
	assertWaiting(t, f.waiting(t), WaitingFiscalYear{Year: 2024, State: WaitingLocked, PeriodID: &locked, Payments: 1})
}

func TestWaitingFiscalYearsLeavesOutReferenceAndHistoricYears(t *testing.T) {
	f := newWaitFixture(t)
	// A reference year, closed or not, is never reported.
	f.period(t, 2025, "closed", true)
	f.kidRow(t, wday(2025, 6, 1))
	f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, Date: wday(2025, 7, 1)})
	// A Stripe row's date is known only in Go, so the reference year is
	// left out there too.
	f.stripeRow(t, "charge", StripeTxnUnbooked, time.Date(2025, 8, 1, 12, 0, 0, 0, time.UTC), true)
	// A year before the books started (the first non-reference year is 2026)
	// is history imported from before, never a missing year.
	f.kidRow(t, wday(2023, 6, 1))
	f.stripeRow(t, "charge", StripeTxnUnbooked, time.Date(2022, 5, 1, 12, 0, 0, 0, time.UTC), true)
	assertWaiting(t, f.waiting(t))
	// A reference year after the books started: its rows pass the date
	// floor, and a Stripe row's date is known only in Go.
	f.period(t, 2029, "closed", true)
	f.stripeRow(t, "charge", StripeTxnUnbooked, time.Date(2029, 3, 1, 12, 0, 0, 0, time.UTC), true)
	assertWaiting(t, f.waiting(t))

	// A year with a period of other dates is never reported missing:
	// CreatePeriod would refuse it.
	f.exec(t, `INSERT INTO fiscal_periods (club_id, year, start_date, end_date) VALUES ($1, 2028, '2028-07-01', '2029-06-30')`, f.club.ID)
	f.kidRow(t, wday(2028, 3, 1))
	assertWaiting(t, f.waiting(t))
}

func TestWaitingFiscalYearsClubWithoutBooks(t *testing.T) {
	f := newWaitFixture(t)
	f.kidRow(t, wday(2027, 1, 2))
	// Only a reference year: the club keeps no books in Brygge.
	f.exec(t, `UPDATE fiscal_periods SET reference_only = true WHERE id = $1`, f.club.PeriodID)
	assertWaiting(t, f.waiting(t))
	// No period at all.
	f.exec(t, `DELETE FROM fiscal_periods WHERE club_id = $1`, f.club.ID)
	assertWaiting(t, f.waiting(t))
	n, err := f.svc.NotifyWaitingFiscalYears(context.Background(), f.club.ID, wday(2027, 2, 1))
	if err != nil || n != 0 {
		t.Fatalf("NotifyWaitingFiscalYears = %d, %v; want nothing queued", n, err)
	}
}

func TestWaitingFiscalYearsStaysInItsClub(t *testing.T) {
	f := newWaitFixture(t)
	other := fin.NewClub(t, f.club.Pool, fin.WithKontoplan(f.svc.SeedKontoplan))
	f.exec(t, `UPDATE clubs SET vipps_auto_assign = true WHERE id = $1`, other.ID)
	otherImp := other.NewBankImport(t)
	inv := other.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	other.NewBankRow(t, otherImp, fin.BankRowOpts{Date: wday(2027, 1, 2), Cents: 45000, KID: inv.KID})
	other.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, Date: wday(2027, 1, 2)})

	assertWaiting(t, f.waiting(t))
	got, err := f.svc.WaitingFiscalYears(context.Background(), other.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertWaiting(t, got, WaitingFiscalYear{Year: 2027, State: WaitingMissing, Payments: 2})
}

func TestWaitingFiscalYearsStripe(t *testing.T) {
	f := newWaitFixture(t)
	created := time.Date(2027, 2, 1, 12, 0, 0, 0, time.UTC)
	// Bookable: a charge naming its order, and a fee (not a payment).
	f.stripeRow(t, "charge", StripeTxnUnbooked, created, true)
	f.stripeRow(t, "fee", StripeTxnUnbooked, created, false)
	// Not bookable by the pass: a payout, an adjustment, a charge with no
	// order, and a charge held in review.
	f.stripeRow(t, "payout", StripeTxnUnbooked, created, false)
	f.stripeRow(t, "other_adjustment", StripeTxnUnbooked, created, false)
	f.stripeRow(t, "charge", StripeTxnUnbooked, created, false)
	// Held in review, even for a reason the pass retries itself: a person
	// decides it, so it does not wait for the year.
	f.stripeRow(t, "charge", "needs_review", created, true)
	assertWaiting(t, f.waiting(t), WaitingFiscalYear{Year: 2027, State: WaitingMissing, Payments: 1})

	// A year held up only by a fee still counts as one.
	f.stripeRow(t, "fee", StripeTxnUnbooked, time.Date(2028, 2, 1, 12, 0, 0, 0, time.UTC), false)
	assertWaiting(t, f.waiting(t),
		WaitingFiscalYear{Year: 2027, State: WaitingMissing, Payments: 1},
		WaitingFiscalYear{Year: 2028, State: WaitingMissing, Payments: 1},
	)
}

// A card payment at 23:30 UTC on 31 December is booked on 1 January in the
// club's zone, so it waits for the new year there and for nothing in UTC.
func TestWaitingFiscalYearsStripeYearBoundary(t *testing.T) {
	f := newWaitFixture(t)
	f.stripeRow(t, "charge", StripeTxnUnbooked, time.Date(2026, 12, 31, 23, 30, 0, 0, time.UTC), true)
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	ctx := context.Background()
	got, err := f.svc.waitingFiscalYears(ctx, f.club.ID, oslo)
	if err != nil {
		t.Fatal(err)
	}
	assertWaiting(t, got, WaitingFiscalYear{Year: 2027, State: WaitingMissing, Payments: 1, Oldest: wday(2027, 1, 1)})
	got, err = f.svc.waitingFiscalYears(ctx, f.club.ID, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	assertWaiting(t, got)
}

func (f *waitFixture) notices(t *testing.T) []FiscalYearWaitingPayload {
	t.Helper()
	rows, err := f.club.Pool.Query(context.Background(),
		`SELECT payload FROM deliveries WHERE club_id = $1 AND kind = $2 ORDER BY created_at, id`,
		f.club.ID, bcast.KindAccountingFiscalYearWaiting)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []FiscalYearWaitingPayload
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var p FiscalYearWaitingPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func TestWaitingYearNoticeQueuedOnce(t *testing.T) {
	ctx := context.Background()
	f := newWaitFixture(t)
	f.kidRow(t, wday(2027, 1, 2))
	now := wday(2027, 1, 10)

	// A year after now's is never notified.
	if n, err := f.svc.NotifyWaitingFiscalYears(ctx, f.club.ID, wday(2026, 12, 31)); err != nil || n != 0 {
		t.Fatalf("notify before the year = %d, %v; want 0", n, err)
	}

	if n, err := f.svc.NotifyWaitingFiscalYears(ctx, f.club.ID, now); err != nil || n != 1 {
		t.Fatalf("first notify = %d, %v; want 1", n, err)
	}
	if n, err := f.svc.NotifyWaitingFiscalYears(ctx, f.club.ID, now); err != nil || n != 0 {
		t.Fatalf("second notify = %d, %v; want 0", n, err)
	}
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.svc.NotifyAllWaitingFiscalYears(ctx, now); err != nil {
				t.Errorf("concurrent notify: %v", err)
			}
		}()
	}
	wg.Wait()
	got := f.notices(t)
	if len(got) != 1 {
		t.Fatalf("notices = %+v, want one", got)
	}
	want := FiscalYearWaitingPayload{Ref: "fiscal_year_waiting:" + f.club.ID + ":2027:missing", Year: 2027, State: WaitingMissing}
	if got[0] != want {
		t.Errorf("payload = %+v, want %+v", got[0], want)
	}

	// Another year or state is a notice of its own.
	closed := f.period(t, 2025, "closed", false)
	f.kidRow(t, wday(2025, 6, 1))
	if n, err := f.svc.NotifyWaitingFiscalYears(ctx, f.club.ID, now); err != nil || n != 1 {
		t.Fatalf("notify of a closed year = %d, %v; want 1", n, err)
	}
	// Reopened and closed again: a new closing, told again.
	if err := f.svc.ReopenPeriod(ctx, f.club.ID, closed); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE fiscal_periods SET status = 'closed', closed_at = now() + interval '1 minute' WHERE id = $1`, closed)
	if n, err := f.svc.NotifyWaitingFiscalYears(ctx, f.club.ID, now); err != nil || n != 1 {
		t.Fatalf("notify after a second closing = %d, %v; want 1", n, err)
	}
	got = f.notices(t)
	if len(got) != 3 || got[1].State != WaitingClosed || got[1].PeriodID != closed || got[1].Ref == got[2].Ref {
		t.Errorf("notices = %+v, want the missing year and two closings of 2025", got)
	}
}

// The down migration removes the notices it can no longer hold, restores the
// narrower check and drops the index; the up applies again over it.
func TestMigration000144DownWithNotices(t *testing.T) {
	ctx := context.Background()
	f := newWaitFixture(t)
	f.kidRow(t, wday(2027, 1, 2))
	if n, err := f.svc.NotifyWaitingFiscalYears(ctx, f.club.ID, wday(2027, 1, 10)); err != nil || n != 1 {
		t.Fatalf("notify = %d, %v", n, err)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(testutil.MigrationsDir(), name)) // #nosec G304 -- test-only, fixed migrations path
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	up, down := read("000144_fiscal_year_waiting_notice.up.sql"), read("000144_fiscal_year_waiting_notice.down.sql")
	f.exec(t, up)
	f.exec(t, down)
	if got := f.notices(t); len(got) != 0 {
		t.Errorf("notices after the down = %+v, want none", got)
	}
	if _, err := f.club.Pool.Exec(ctx,
		`INSERT INTO deliveries (kind, club_id, email, status) VALUES ('accounting_fiscal_year_waiting', $1, '', 'pending')`,
		f.club.ID); err == nil {
		t.Errorf("the restored check admits an accounting_* row")
	}
	f.exec(t, down)
	f.exec(t, up)
	if n, err := f.svc.NotifyWaitingFiscalYears(ctx, f.club.ID, wday(2027, 1, 10)); err != nil || n != 1 {
		t.Fatalf("notify after down and up = %d, %v; want 1", n, err)
	}
}

// Only what a pass would book counts: a KID row whose faktura the cascade
// finds, and a Vipps charge a pass takes up on this club's settings.
func TestWaitingFiscalYearsCountsOnlyWhatAPassBooks(t *testing.T) {
	ctx := context.Background()
	f := newWaitFixture(t)
	jan2 := wday(2027, 1, 2)
	// Bookable: a KID of an owed faktura.
	f.kidRow(t, jan2)
	// Not bookable: a KID no faktura carries, a KID of a faktura already
	// paid, a KID a booked bank row already carries, a rejected-KID notice.
	f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: jan2, Cents: 45000, KID: "000000099993"})
	paid := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Paid: true})
	f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: jan2, Cents: 45000, KID: paid.KID})
	carried := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: jan2, Cents: 45000, KID: carried.KID})
	booked := f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: wday(2026, 12, 1), Cents: 45000, KID: carried.KID})
	var entry string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO journal_entries (club_id, fiscal_period_id, entry_number, entry_date, description, source, created_by)
		 VALUES ($1, $2, 9001, '2026-12-01', 'Synthetic', 'bank_import', $3) RETURNING id`,
		f.club.ID, f.club.PeriodID, f.club.Treasurer.ID).Scan(&entry); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE bank_import_rows SET journal_entry_id = $2 WHERE id = $1`, booked, entry)
	rejected := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: jan2, Cents: 45000, KID: rejected.KID,
		Description: "KID " + rejected.KID + " ble ikke akseptert på denne kontoen"})
	assertWaiting(t, f.waiting(t), WaitingFiscalYear{Year: 2027, State: WaitingMissing, Payments: 1})

	// With Vipps auto-assign off only an order-linked charge is booked.
	f.exec(t, `UPDATE clubs SET vipps_auto_assign = false WHERE id = $1`, f.club.ID)
	f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, Date: wday(2028, 1, 2)})
	assertWaiting(t, f.waiting(t), WaitingFiscalYear{Year: 2027, State: WaitingMissing, Payments: 1})
	order := f.club.NewOrderForInvoice(t, f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 30000}))
	f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, Date: wday(2028, 1, 3), OrderID: order.VippsReference})
	assertWaiting(t, f.waiting(t),
		WaitingFiscalYear{Year: 2027, State: WaitingMissing, Payments: 1},
		WaitingFiscalYear{Year: 2028, State: WaitingMissing, Payments: 1, Oldest: wday(2028, 1, 3)},
	)
	// Auto-assign on: the plain charge counts too.
	f.exec(t, `UPDATE clubs SET vipps_auto_assign = true WHERE id = $1`, f.club.ID)
	assertWaiting(t, f.waiting(t),
		WaitingFiscalYear{Year: 2027, State: WaitingMissing, Payments: 1},
		WaitingFiscalYear{Year: 2028, State: WaitingMissing, Payments: 2, Oldest: wday(2028, 1, 2)},
	)
}

// On the legacy Vipps model a settlement deposit is booked by bank sync's
// settlement confirm, which is not counted; the per-charge payout sweep's is.
func TestWaitingFiscalYearsVippsDepositOnlyOnPerCharge(t *testing.T) {
	f := newWaitFixture(t)
	f.exec(t, `UPDATE clubs SET vipps_auto_assign = false, vipps_per_charge_enabled = false WHERE id = $1`, f.club.ID)
	f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: wday(2027, 1, 5), Cents: 19000, Description: "Utb. 4000001 Vippsnr 000001"})
	// A charge of that settlement, left to the confirm on the legacy model.
	f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 20000, Date: wday(2027, 1, 4), Settlement: "4000001"})
	assertWaiting(t, f.waiting(t))
	f.exec(t, `UPDATE clubs SET vipps_per_charge_enabled = true WHERE id = $1`, f.club.ID)
	assertWaiting(t, f.waiting(t), WaitingFiscalYear{Year: 2027, State: WaitingMissing, Payments: 1})
}

// A notice that failed without reaching anybody is queued again while the
// condition holds; one sent, or with an unknown outcome, never is.
func TestWaitingYearNoticeRearmedOnlyAfterANonDelivery(t *testing.T) {
	ctx := context.Background()
	now := wday(2027, 1, 10)
	cases := []struct {
		name, status, errText string
		rearmed               bool
	}{
		// Only the "condition cleared" failure, matched exactly.
		{"resolved before sending", bcast.DeliveryFailed, ErrFiscalYearResolved.Error(), true},
		// Every other failure stays for a person (or RequeueUnaddressed).
		{"no address", bcast.DeliveryFailed, "delivery cannot be sent: no treasurer or board notification address configured", false},
		{"no transport", bcast.DeliveryFailed, "delivery cannot be sent: no mail transport configured", false},
		{"retries used up", bcast.DeliveryFailed, "smtp: 451 4.3.0 try again later", false},
		{"outcome unknown", bcast.DeliveryFailed, "smtp: email delivery outcome unknown", false},
		{"sent", bcast.DeliverySent, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newWaitFixture(t)
			f.kidRow(t, wday(2027, 1, 2))
			if n, err := f.svc.NotifyWaitingFiscalYears(ctx, f.club.ID, now); err != nil || n != 1 {
				t.Fatalf("first notify = %d, %v", n, err)
			}
			f.exec(t, `UPDATE deliveries SET status = $2, error = $3, attempts = 1 WHERE club_id = $1`, f.club.ID, tc.status, tc.errText)
			n, err := f.svc.NotifyWaitingFiscalYears(ctx, f.club.ID, now)
			if err != nil {
				t.Fatal(err)
			}
			var status, errText string
			var attempts int
			if err := f.club.Pool.QueryRow(ctx,
				`SELECT status, error, attempts FROM deliveries WHERE club_id = $1`, f.club.ID).Scan(&status, &errText, &attempts); err != nil {
				t.Fatal(err)
			}
			if tc.rearmed {
				if n != 1 || status != bcast.DeliveryPending || errText != "" || attempts != 0 {
					t.Fatalf("after notify: n=%d status=%s error=%q attempts=%d; want re-armed", n, status, errText, attempts)
				}
				// Pending now: the next run leaves it alone.
				if n, err := f.svc.NotifyWaitingFiscalYears(ctx, f.club.ID, now); err != nil || n != 0 {
					t.Fatalf("third notify = %d, %v; want 0", n, err)
				}
				return
			}
			if n != 0 || status != tc.status {
				t.Fatalf("after notify: n=%d status=%s; want left as %s", n, status, tc.status)
			}
		})
	}
}

// The cascade books by more than the KID column: arkivref, a faktura number
// in the text, and payer name and amount while bank auto-assign is off.
func TestWaitingFiscalYearsCountsEveryCascadeMatch(t *testing.T) {
	ctx := context.Background()
	f := newWaitFixture(t)
	jan := func(d int) time.Time { return wday(2027, 1, d) }

	byNumber := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: jan(2), Cents: 45000,
		Description: fmt.Sprintf("Faktura %d", byNumber.Number)})
	byRef := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: jan(3), Cents: 45000, Reference: byRef.KID})
	payer := f.club.NewMember(t, "member")
	f.exec(t, `UPDATE users SET first_name = 'Synthetic', last_name = 'Namepayer' WHERE id = $1`, payer.ID)
	f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 51700, Member: &payer})
	f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: jan(4), Cents: 51700, Counterpart: "Synthetic Namepayer"})
	// Two rows for one faktura: the cascade books one; the other stays.
	twice := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: jan(5), Cents: 45000, KID: twice.KID})
	f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: jan(6), Cents: 45000, KID: twice.KID})

	assertWaiting(t, f.waiting(t), WaitingFiscalYear{Year: 2027, State: WaitingMissing, Payments: 4})

	// With bank auto-assign on, sync's cascade does not match by name.
	f.exec(t, `UPDATE clubs SET bank_auto_assign = true WHERE id = $1`, f.club.ID)
	assertWaiting(t, f.waiting(t), WaitingFiscalYear{Year: 2027, State: WaitingMissing, Payments: 3})
	_ = ctx
}

// A failed faktura lookup is an error, never "nothing to book": the
// cascade's own lookup (findCascadeInvoice) swallows it, its twin reports it.
func TestWaitingFiscalYearsReportsALookupError(t *testing.T) {
	f := newWaitFixture(t)
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	row := BankRow{KID: inv.KID, Description: "Faktura 1", Counterpart: "Synthetic Payer", Amount: 450}
	if id, err := f.svc.cascadeInvoice(context.Background(), f.club.ID, row, true); err != nil || id != inv.ID {
		t.Fatalf("cascadeInvoice = %q, %v; want %s", id, err, inv.ID)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	id, err := f.svc.cascadeInvoice(ctx, f.club.ID, BankRow{KID: "000000099993", Description: "Faktura 1", Counterpart: "Synthetic Payer", Amount: 450}, true)
	if err == nil || id != "" {
		t.Fatalf("cascadeInvoice on a cancelled context = %q, %v; want an error", id, err)
	}
	if got := f.svc.findCascadeInvoice(ctx, f.club.ID, row, true); got != "" {
		t.Fatalf("findCascadeInvoice on a cancelled context = %q, want empty as before", got)
	}
}
