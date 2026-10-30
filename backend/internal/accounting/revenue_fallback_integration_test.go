package accounting

import (
	"context"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// unmappedCategory is a price-item category the revenue map does not know.
const unmappedCategory = "synthetic_unmapped"

// A price item whose category the map does not know must book to the same
// fallback account whether it is invoiced or paid by Vipps.
func TestUnmappedCategoryBooksToFallbackOnEveryPath(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()

	// fin price items cost 100 kr, so the Vipps matcher finds exactly this one.
	id := f.club.NewInvoice(t, fin.InvoiceOpts{
		Number: 3101,
		Member: &fin.Member{ID: f.userID},
		Lines:  []fin.Line{{Category: unmappedCategory, Cents: 10000}},
	}).ID
	if res := f.sync(t); res.Synced != 1 {
		t.Fatalf("sync = %+v, want synced=1", res)
	}
	lines := f.entryLines(t, id)
	if lines["1500"] != [2]float64{100, 0} || lines["3900"] != [2]float64{0, 100} {
		t.Errorf("invoice booking = %v, want DR 1500 / CR 3900 100", lines)
	}

	code, label, err := f.svc.matchVippsAmountToPriceArticle(ctx, f.clubID, 100)
	if err != nil {
		t.Fatalf("matchVippsAmountToPriceArticle: %v", err)
	}
	if label == "" {
		t.Fatal("Vipps matcher found no price item for 100 kr")
	}
	if code != "3900" {
		t.Errorf("Vipps per-charge account = %s, want 3900", code)
	}

	split := buildOrderChargeSplit(nil, []orderChargeLine{{Category: unmappedCategory, Total: 100}})
	if len(split) != 1 || split[0].AccountCode != "3900" {
		t.Errorf("order split = %+v, want one credit on 3900", split)
	}

	// Never auto-booked: the known form refuses the category.
	if code, ok := RevenueAccount(unmappedCategory); ok {
		t.Errorf("RevenueAccount(%q) = %s, want unknown", unmappedCategory, code)
	}
}

// A faktura total that differs from its line sum books the difference to the
// fallback account so the bilag balances and the unexplained money is visible.
func TestInvoiceResidualBooksToFallback(t *testing.T) {
	f := newSyncFixture(t)
	id := f.invoice(t, 3201, 2500, "open", true, "")
	if _, err := f.pool.Exec(context.Background(),
		`UPDATE invoices SET total_amount = 2550 WHERE id = $1`, id); err != nil {
		t.Fatalf("raise total: %v", err)
	}
	if res := f.sync(t); res.Synced != 1 {
		t.Fatalf("sync = %+v, want synced=1", res)
	}
	lines := f.entryLines(t, id)
	if lines["1500"] != [2]float64{2550, 0} {
		t.Errorf("1500 = %v, want DR 2550", lines["1500"])
	}
	if lines["3120"] != [2]float64{0, 2500} {
		t.Errorf("3120 = %v, want CR 2500 for the slip line", lines["3120"])
	}
	if lines["3900"] != [2]float64{0, 50} {
		t.Errorf("3900 = %v, want CR 50 residual", lines["3900"])
	}
	if _, ok := lines["3100"]; ok {
		t.Errorf("residual booked to 3100: %v", lines)
	}
}
