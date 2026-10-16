package accounting

import (
	"context"
	"errors"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

func (f *syncFixture) syncEntries(t *testing.T, invoiceID string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM journal_entries WHERE source = 'invoice_sync' AND source_id = $1`, invoiceID,
	).Scan(&n); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	return n
}

func TestBookInvoiceSplitsAndIsIdempotent(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Lines: []fin.Line{
		{Category: "slip_fee", Cents: 250000},
		{Category: "membership", Cents: 45000},
	}})

	got, err := f.svc.BookInvoice(ctx, f.clubID, inv.ID, f.userID)
	if err != nil || got != InvoiceBooked {
		t.Fatalf("BookInvoice = %q, %v; want booked", got, err)
	}
	lines := f.entryLines(t, inv.ID)
	if lines["1500"] != [2]float64{2950, 0} || lines["3120"] != [2]float64{0, 2500} || lines["3100"] != [2]float64{0, 450} {
		t.Errorf("booking = %v, want DR 1500 2950 / CR 3120 2500 / CR 3100 450", lines)
	}

	// A resend, a double click or the repair sync must not book twice.
	if got, err := f.svc.BookInvoice(ctx, f.clubID, inv.ID, f.userID); err != nil || got != InvoiceAlreadyBooked {
		t.Errorf("second BookInvoice = %q, %v; want already_booked", got, err)
	}
	if res := f.sync(t); res.Synced != 0 {
		t.Errorf("repair sync re-booked a booked faktura: %+v", res)
	}
	if n := f.syncEntries(t, inv.ID); n != 1 {
		t.Errorf("entries = %d, want 1", n)
	}
}

func TestBookInvoiceCreditNoteFlipsSides(t *testing.T) {
	f := newSyncFixture(t)
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: -250000})
	if got, err := f.svc.BookInvoice(context.Background(), f.clubID, inv.ID, f.userID); err != nil || got != InvoiceBooked {
		t.Fatalf("BookInvoice = %q, %v", got, err)
	}
	lines := f.entryLines(t, inv.ID)
	if lines["1500"] != [2]float64{0, 2500} || lines["3120"] != [2]float64{2500, 0} {
		t.Errorf("credit note booking = %v, want DR 3120 / CR 1500 2500", lines)
	}
}

func TestBookInvoiceRefusesDraftsAndVoided(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	draft := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Unsent: true})
	voided := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Status: "voided"})
	for name, id := range map[string]string{"draft": draft.ID, "voided": voided.ID} {
		if _, err := f.svc.BookInvoice(ctx, f.clubID, id, f.userID); err == nil {
			t.Errorf("%s: BookInvoice succeeded, want an error", name)
		}
		if n := f.syncEntries(t, id); n != 0 {
			t.Errorf("%s: booked %d entries", name, n)
		}
	}
}

// The send has already happened when booking runs, so a closed period is
// reported (for the operator) rather than fatal; once the period reopens
// the repair sync books it.
func TestBookInvoiceClosedPeriodIsReportedAndRepairable(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	if _, err := f.pool.Exec(ctx, `UPDATE fiscal_periods SET status = 'closed' WHERE id = $1`, f.periodID); err != nil {
		t.Fatalf("close period: %v", err)
	}
	if _, err := f.svc.BookInvoice(ctx, f.clubID, inv.ID, f.userID); !errors.Is(err, ErrInvoicePeriodClosed) {
		t.Fatalf("BookInvoice in closed period = %v, want ErrInvoicePeriodClosed", err)
	}
	if n := f.syncEntries(t, inv.ID); n != 0 {
		t.Fatalf("booked into a closed period")
	}

	if _, err := f.pool.Exec(ctx, `UPDATE fiscal_periods SET status = 'open' WHERE id = $1`, f.periodID); err != nil {
		t.Fatalf("reopen period: %v", err)
	}
	if res := f.sync(t); res.Synced != 1 {
		t.Errorf("repair sync = %+v, want the missed faktura booked", res)
	}
}

func TestReverseInvoiceNetsToZero(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 250000})
	if _, err := f.svc.BookInvoice(ctx, f.clubID, inv.ID, f.userID); err != nil {
		t.Fatalf("BookInvoice: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE invoices SET status = 'voided' WHERE id = $1`, inv.ID); err != nil {
		t.Fatalf("void: %v", err)
	}

	reversed, err := f.svc.ReverseInvoice(ctx, f.clubID, inv.ID, f.userID)
	if err != nil || !reversed {
		t.Fatalf("ReverseInvoice = %v, %v; want reversed", reversed, err)
	}
	if got := f.net(t, "1500"); got != 0 {
		t.Errorf("1500 net = %v, want 0", got)
	}
	if got := f.net(t, "3120"); got != 0 {
		t.Errorf("3120 net = %v, want 0", got)
	}
	if again, err := f.svc.ReverseInvoice(ctx, f.clubID, inv.ID, f.userID); err != nil || again {
		t.Errorf("second ReverseInvoice = %v, %v; want nothing to reverse", again, err)
	}
	if res := f.sync(t); res.Reversed != 0 || res.Synced != 0 {
		t.Errorf("repair sync after a same-flow reversal = %+v, want a no-op", res)
	}
}

func TestReverseInvoiceRefusesClosedPeriod(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	if _, err := f.svc.BookInvoice(ctx, f.clubID, inv.ID, f.userID); err != nil {
		t.Fatalf("BookInvoice: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE fiscal_periods SET status = 'closed' WHERE id = $1`, f.periodID); err != nil {
		t.Fatalf("close period: %v", err)
	}
	if _, err := f.svc.ReverseInvoice(ctx, f.clubID, inv.ID, f.userID); !errors.Is(err, ErrInvoicePeriodClosed) {
		t.Errorf("ReverseInvoice in closed period = %v, want ErrInvoicePeriodClosed", err)
	}
}

// The unique index is the backstop when two paths race past the existence
// check: the second booking is reported as already booked, not an error.
func TestInvoiceSyncUniqueIndex(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	if _, err := f.svc.BookInvoice(ctx, f.clubID, inv.ID, f.userID); err != nil {
		t.Fatalf("BookInvoice: %v", err)
	}
	err := f.svc.bookInvoiceEntry(ctx, f.clubID, f.periodID, inv.ID, inv.Number, 450, "2026-03-01", nil, f.userID)
	if !isUniqueViolation(err) {
		t.Errorf("second bookInvoiceEntry = %v, want a unique violation", err)
	}
}
