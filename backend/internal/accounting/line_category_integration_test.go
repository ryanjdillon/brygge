package accounting

import (
	"context"
	"strings"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// recategorise moves the club's synthetic price item for `from` to `to`.
func (f *syncFixture) recategorise(t *testing.T, from, to string) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(),
		`UPDATE price_items SET category = $1 WHERE id = $2`, to, f.club.PriceItem(t, from),
	); err != nil {
		t.Fatalf("re-categorise %s: %v", from, err)
	}
}

// An issued faktura books by the category stored on its lines: after its
// price item is re-categorised, a later sync, a resplit and a void all keep
// using the account of the category it was issued under (BRY-389).
func TestIssuedFakturaBooksByStoredCategory(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()

	booked := f.invoice(t, 3901, 2500, "open", true, "")
	if res := f.sync(t); res.Synced != 1 {
		t.Fatalf("first sync = %+v, want synced=1", res)
	}
	misSplit := f.invoice(t, 3902, 2500, "open", true, "")
	f.bookMisSplit(t, misSplit, 2500)
	unsent := f.invoice(t, 3903, 2500, "open", false, "")

	// slip_fee books to 3120, membership to 3100.
	f.recategorise(t, "slip_fee", "membership")

	if _, err := f.pool.Exec(ctx, `UPDATE invoices SET sent_at = now() WHERE id = $1`, unsent); err != nil {
		t.Fatalf("send: %v", err)
	}
	if res := f.sync(t); res.Synced != 1 {
		t.Fatalf("resync = %+v, want synced=1", res)
	}
	if lines := f.entryLines(t, unsent); lines["3120"] != [2]float64{0, 2500} {
		t.Errorf("faktura sent after the re-categorisation booked %v, want CR 3120 2500", lines)
	}

	// The resplit moves only the legacy 3100 booking, onto 3120; the
	// correctly booked fakturas are left where they are.
	res := f.resplit(t, true)
	if len(res.Invoices) != 1 || res.Invoices[0].InvoiceNumber != 3902 {
		t.Fatalf("resplit = %+v, want one correction for faktura 3902", res)
	}
	if got := f.net(t, "3100"); got != 0 {
		t.Errorf("3100 net = %v, want 0", got)
	}
	if got := f.net(t, "3120"); got != -7500 {
		t.Errorf("3120 net = %v, want -7500 (three slip fakturas)", got)
	}

	// Voiding reverses what was booked.
	if _, err := f.pool.Exec(ctx, `UPDATE invoices SET status = 'voided' WHERE id = $1`, booked); err != nil {
		t.Fatalf("void: %v", err)
	}
	if res := f.sync(t); res.Reversed != 1 {
		t.Fatalf("sync after void = %+v, want reversed=1", res)
	}
	if got := f.net(t, "3120"); got != -5000 {
		t.Errorf("3120 net after void = %v, want -5000", got)
	}
	if got := f.net(t, "3100"); got != 0 {
		t.Errorf("3100 net after void = %v, want 0", got)
	}
}

// The schema refuses a price-item line without a category.
func TestInvoiceLineWithPriceItemRequiresCategory(t *testing.T) {
	f := newSyncFixture(t)
	inv := f.invoice(t, 3911, 100, "open", false, "")
	_, err := f.pool.Exec(context.Background(),
		`INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total, price_item_id)
		 VALUES ($1, 'Synthetic uncategorised', 1, 100, 100, $2)`,
		inv, f.club.PriceItem(t, "slip_fee"),
	)
	if err == nil || !strings.Contains(err.Error(), "invoice_lines_price_item_category") {
		t.Errorf("insert without category: err = %v, want the CHECK to refuse it", err)
	}
}

// A line written before every creation path stored its category resolves
// through its price item. The constraint now refuses such a line, so the
// test drops it for its own schema and restores it afterwards; it does not
// run in parallel.
func TestUncategorisedOldLineFallsBackToItemCategory(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx,
		`ALTER TABLE invoice_lines DROP CONSTRAINT invoice_lines_price_item_category`); err != nil {
		t.Fatalf("drop constraint: %v", err)
	}
	t.Cleanup(func() {
		if _, err := f.pool.Exec(ctx, `DELETE FROM invoice_lines WHERE price_item_id IS NOT NULL AND category IS NULL`); err != nil {
			t.Errorf("remove old-style line: %v", err)
		}
		if _, err := f.pool.Exec(ctx,
			`ALTER TABLE invoice_lines ADD CONSTRAINT invoice_lines_price_item_category
			     CHECK (price_item_id IS NULL OR category IS NOT NULL)`); err != nil {
			t.Errorf("restore constraint: %v", err)
		}
	})

	inv := f.club.NewInvoice(t, fin.InvoiceOpts{
		Number: 3921, Member: &fin.Member{ID: f.userID},
		Lines: []fin.Line{{Category: "slip_fee", Cents: 250000}},
	}).ID
	if _, err := f.pool.Exec(ctx, `UPDATE invoice_lines SET category = NULL WHERE invoice_id = $1`, inv); err != nil {
		t.Fatalf("clear category: %v", err)
	}
	if res := f.sync(t); res.Synced != 1 {
		t.Fatalf("sync = %+v, want synced=1", res)
	}
	if lines := f.entryLines(t, inv); lines["3120"] != [2]float64{0, 2500} {
		t.Errorf("old-style line booked %v, want CR 3120 2500 from the item's category", lines)
	}
}
