package accounting

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// mixedInvoice books a sent faktura with a slip-rent line, a membership line
// and a custom line on 3900, the way the pre-split sync did: all revenue on
// 3100. A resplit then moves it onto 3120/3100/3900.
func (f *syncFixture) mixedResplitInvoice(t *testing.T) fin.Invoice {
	t.Helper()
	m := f.club.NewMember(t, "member")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Lines: []fin.Line{
		{Category: "slip_fee", Cents: 255000},
		{Category: "membership", Cents: 45000},
		{AccountCode: "3900", Cents: 20000},
	}})
	f.bookMisSplit(t, inv.ID, fin.Kr(inv.TotalCents))
	if res := f.resplit(t, true); res.Posted != 1 {
		t.Fatalf("resplit = %+v, want one correction", res)
	}
	return inv
}

func (f *syncFixture) assertAllNetZero(t *testing.T, codes ...string) {
	t.Helper()
	for _, code := range codes {
		if got := f.net(t, code); got != 0 {
			t.Errorf("%s net = %v, want 0", code, got)
		}
	}
}

// Voiding a faktura reverses its whole entry family: the sync entry and the
// resplit correction. Reversing only the sync entry would leave the
// correction's +/- per account standing forever.
func TestReverseInvoiceReversesResplitCorrections(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	inv := f.mixedResplitInvoice(t)
	if got := f.net(t, "3120"); got != -2550 {
		t.Fatalf("3120 before void = %v, want -2550 after the resplit", got)
	}

	f.exec(t, `UPDATE invoices SET status = 'voided' WHERE id = $1`, inv.ID)
	reversals, err := f.svc.ReverseInvoice(ctx, f.clubID, inv.ID, f.userID)
	if err != nil || len(reversals) != 2 {
		t.Fatalf("ReverseInvoice = %v, %v; want the sync entry and the resplit reversed", reversals, err)
	}
	f.assertAllNetZero(t, "1500", "3100", "3120", "3900")
	if res := f.resplit(t, false); len(res.Invoices) != 0 {
		t.Errorf("resplit after void plans %+v, want nothing", res.Invoices)
	}
}

// The repair sync reverses the same family when the void happened without
// the in-flow reversal, and counts the faktura once.
func TestRepairSyncReversesResplitFamily(t *testing.T) {
	f := newSyncFixture(t)
	inv := f.mixedResplitInvoice(t)
	f.exec(t, `UPDATE invoices SET status = 'voided' WHERE id = $1`, inv.ID)

	res := f.sync(t)
	if res.Reversed != 1 {
		t.Fatalf("repair sync = %+v, want one faktura reversed", res)
	}
	f.assertAllNetZero(t, "1500", "3100", "3120", "3900")
}

// If any entry of the family sits in a closed period, nothing is reversed:
// a partial reversal would leave the books split across years.
func TestReverseInvoiceFamilyRefusesClosedPeriod(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	inv := f.mixedResplitInvoice(t)
	f.exec(t, `UPDATE fiscal_periods SET status = 'closed' WHERE id = $1`, f.periodID)

	if rev, err := f.svc.ReverseInvoice(ctx, f.clubID, inv.ID, f.userID); !errors.Is(err, ErrInvoicePeriodClosed) || len(rev) != 0 {
		t.Fatalf("ReverseInvoice in closed period = %v, %v; want ErrInvoicePeriodClosed and nothing reversed", rev, err)
	}
	var posted int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM journal_entries
		  WHERE source IN ('invoice_sync', 'invoice_resplit') AND source_id = $1 AND status = 'posted'`,
		inv.ID).Scan(&posted); err != nil || posted != 2 {
		t.Errorf("posted family entries = %d (err %v), want both still posted", posted, err)
	}
}

// A resplit of a closed period is refused outright.
func TestResplitRefusesClosedPeriod(t *testing.T) {
	f := newSyncFixture(t)
	m := f.club.NewMember(t, "member")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Cents: 255000})
	f.bookMisSplit(t, inv.ID, fin.Kr(inv.TotalCents))
	f.exec(t, `UPDATE fiscal_periods SET status = 'closed' WHERE id = $1`, f.periodID)
	if _, err := f.svc.ResplitInvoiceBilags(context.Background(), f.clubID, f.periodID, f.userID, true); err == nil {
		t.Errorf("resplit of a closed period succeeded")
	}
}

// A multi-line faktura with a custom-account line and a line sum that falls
// short of the header total books the residual to 3900 alongside the custom
// line and still balances.
func TestBookInvoiceCustomLineAndResidual(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	m := f.club.NewMember(t, "member")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Lines: []fin.Line{
		{Category: "slip_fee", Cents: 255000},
		{AccountCode: "3900", Cents: 20000},
	}})
	f.exec(t, `UPDATE invoices SET total_amount = total_amount + 1.50 WHERE id = $1`, inv.ID)

	if _, err := f.svc.BookInvoice(ctx, f.clubID, inv.ID, f.userID); err != nil {
		t.Fatalf("BookInvoice: %v", err)
	}
	lines := f.entryLines(t, inv.ID)
	want := map[string][2]float64{"1500": {2751.50, 0}, "3120": {0, 2550}, "3900": {0, 201.50}, "3100": {0, 0}}
	for code, w := range want {
		if lines[code] != w {
			t.Errorf("%s = %v, want %v (all lines %v)", code, lines[code], w, lines)
		}
	}
}

// A price item archived (or recategorised) after the faktura was issued
// still books by the category it has at booking time, without failing.
func TestBookInvoiceArchivedPriceItem(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	m := f.club.NewMember(t, "member")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Lines: []fin.Line{{Category: "motorhome", Cents: 90000}}})
	f.exec(t, `UPDATE price_items SET is_active = false WHERE id = $1`, f.club.PriceItem(t, "motorhome"))

	if _, err := f.svc.BookInvoice(ctx, f.clubID, inv.ID, f.userID); err != nil {
		t.Fatalf("BookInvoice with an archived price item: %v", err)
	}
	if lines := f.entryLines(t, inv.ID); lines["3210"] != [2]float64{0, 900} {
		t.Errorf("archived motorhome line booked as %v, want CR 3210 900", lines)
	}
}

// Sending a faktura and a repair sync racing each other book it once: the
// loser hits the unique index and reports it as already booked.
func TestConcurrentSendAndSyncBookOnce(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	m := f.club.NewMember(t, "member")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Cents: 45000})

	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 3; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := f.svc.BookInvoice(ctx, f.clubID, inv.ID, f.userID); err != nil {
				errs <- err
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := f.svc.SyncInvoices(ctx, f.clubID, f.periodID, f.userID); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent booking: %v", err)
	}
	if n := f.syncEntries(t, inv.ID); n != 1 {
		t.Errorf("invoice_sync entries = %d, want exactly 1", n)
	}
	if got := f.net(t, "1500"); got != 450 {
		t.Errorf("1500 net = %v, want 450 (booked once)", got)
	}
}

func (f *syncFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}
