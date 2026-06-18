package handlers

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

// TestPriceItemSummaryIncludesOrphanedLines locks in the DIL-399 fix: an
// invoice line whose price_item_id is NULL (no price item, e.g. an
// imported invoice) must still be counted in billed/overdue/outstanding
// totals and surfaced as a catch-all bucket row — not silently dropped
// because the aggregation used to drive FROM price_items.
func TestPriceItemSummaryIncludesOrphanedLines(t *testing.T) {
	testutil.SkipIfNoDB(t)
	db := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, db)
	userID, _ := testutil.SeedUser(t, db, clubID, []string{"treasurer"})
	periodID := seedFiscalPeriod(t, db, clubID, 2026)

	// One real price item, referenced by a normal line.
	var priceItemID string
	if err := db.QueryRow(ctx,
		`INSERT INTO price_items (club_id, category, name, amount)
		 VALUES ($1, 'membership', 'Medlemskontingent', 1000)
		 RETURNING id`,
		clubID,
	).Scan(&priceItemID); err != nil {
		t.Fatalf("seeding price item: %v", err)
	}

	// A single unpaid, overdue invoice with two lines: one mapped to the
	// price item (1000), one orphaned with a NULL price_item_id (3714).
	dueInPast := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var invoiceID string
	if err := db.QueryRow(ctx,
		`INSERT INTO invoices
		   (club_id, user_id, invoice_number, kid_number, due_date,
		    total_amount, fiscal_period_id, recipient_kind, recipient_email, sent_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 'private', 'test@example.com', now())
		 RETURNING id`,
		clubID, userID, 1001, "000010010008", dueInPast, 4714.00, periodID,
	).Scan(&invoiceID); err != nil {
		t.Fatalf("inserting invoice: %v", err)
	}

	if _, err := db.Exec(ctx,
		`INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total, price_item_id)
		 VALUES ($1, 'Medlemskontingent 2026', 1, 1000, 1000, $2)`,
		invoiceID, priceItemID,
	); err != nil {
		t.Fatalf("inserting mapped line: %v", err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total, price_item_id)
		 VALUES ($1, 'Importert faktura 2024', 1, 3714, 3714, NULL)`,
		invoiceID,
	); err != nil {
		t.Fatalf("inserting orphaned line: %v", err)
	}

	h := NewFinancialsHandler(db, testConfig(), nil, zerolog.Nop())
	r := setupRoleProtectedRouter(http.MethodGet, "/admin/financials/price-item-summary",
		h.HandleGetPriceItemSummary, "treasurer", "board", "admin")

	token := generateTestToken(userID, clubID, []string{"treasurer"})
	req := httptest.NewRequest(http.MethodGet, "/admin/financials/price-item-summary", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp priceItemSummaryResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	// Totals must include the orphaned 3714 line — the whole point of the
	// fix. Pre-DIL-399 these would each read 1000.
	approxEq(t, "totals.billed", resp.Totals.Billed, 4714)
	approxEq(t, "totals.overdue", resp.Totals.Overdue, 4714)
	approxEq(t, "totals.outstanding", resp.Totals.Outstanding, 4714)

	// The breakdown must carry a catch-all bucket (empty price_item_id)
	// holding the orphaned line, alongside the mapped row.
	var mapped, orphan *priceItemSummaryRow
	for i := range resp.Items {
		if resp.Items[i].PriceItemID == "" {
			orphan = &resp.Items[i]
		} else if resp.Items[i].PriceItemID == priceItemID {
			mapped = &resp.Items[i]
		}
	}
	if mapped == nil {
		t.Fatalf("expected a row for the mapped price item; got %+v", resp.Items)
	}
	if orphan == nil {
		t.Fatalf("expected a catch-all row for the orphaned line; got %+v", resp.Items)
	}
	approxEq(t, "mapped.billed", mapped.Billed, 1000)
	approxEq(t, "orphan.billed", orphan.Billed, 3714)
	approxEq(t, "orphan.overdue", orphan.Overdue, 3714)
}

func approxEq(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.01 {
		t.Errorf("%s = %.2f, want %.2f", label, got, want)
	}
}
