package accounting

import (
	"context"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/rs/zerolog"
)

// A multi-category invoice should produce a balanced bilag with one
// DR line on 1500 receivables and one CR line per price-item category.
func TestSyncInvoicesSplitsRevenuePerCategory(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"treasurer"})
	svc := NewService(pool, nil, zerolog.Nop())
	if _, err := svc.SeedKontoplan(ctx, clubID); err != nil {
		t.Fatalf("seed kontoplan: %v", err)
	}

	var periodID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, 2026, '2026-01-01', '2026-12-31', 'open') RETURNING id`,
		clubID,
	).Scan(&periodID); err != nil {
		t.Fatalf("fiscal period: %v", err)
	}

	// Two price items in different categories so the resulting bilag
	// must split CR across two revenue accounts.
	var harborItem, slipItem string
	if err := pool.QueryRow(ctx,
		`INSERT INTO price_items (club_id, category, name, amount, unit, is_active)
		 VALUES ($1, 'harbor_membership', 'Havneavgift 2026', 800, 'year', true) RETURNING id`,
		clubID,
	).Scan(&harborItem); err != nil {
		t.Fatalf("harbor item: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO price_items (club_id, category, name, amount, unit, is_active)
		 VALUES ($1, 'slip_fee', 'Plassleie A1', 2500, 'season', true) RETURNING id`,
		clubID,
	).Scan(&slipItem); err != nil {
		t.Fatalf("slip item: %v", err)
	}

	var invoiceID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (club_id, user_id, invoice_number, issue_date, due_date, total_amount, status, kid_number)
		 VALUES ($1, $2, 1001, '2026-03-01', '2026-03-15', 3300, 'sent', '1001') RETURNING id`,
		clubID, userID,
	).Scan(&invoiceID); err != nil {
		t.Fatalf("invoice: %v", err)
	}
	for _, line := range []struct {
		priceItemID string
		amount      float64
		desc        string
	}{
		{harborItem, 800, "Havneavgift"},
		{slipItem, 2500, "Plassleie A1"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total, price_item_id)
			 VALUES ($1, $2, 1, $3, $3, $4)`,
			invoiceID, line.desc, line.amount, line.priceItemID,
		); err != nil {
			t.Fatalf("invoice line: %v", err)
		}
	}

	res, err := svc.SyncInvoices(ctx, clubID, periodID, userID)
	if err != nil {
		t.Fatalf("SyncInvoices: %v", err)
	}
	if res.Synced != 1 {
		t.Fatalf("synced = %d, want 1 (skipped=%d)", res.Synced, res.Skipped)
	}

	var entryID string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM journal_entries WHERE club_id = $1 AND source = 'invoice_sync'`,
		clubID,
	).Scan(&entryID); err != nil {
		t.Fatalf("find entry: %v", err)
	}

	rows, err := pool.Query(ctx,
		`SELECT a.code, jl.debit, jl.credit
		 FROM journal_lines jl
		 JOIN accounts a ON a.id = jl.account_id
		 WHERE jl.journal_entry_id = $1
		 ORDER BY a.code`,
		entryID,
	)
	if err != nil {
		t.Fatalf("lines: %v", err)
	}
	defer rows.Close()

	type line struct {
		code         string
		debit, credit float64
	}
	var lines []line
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.code, &l.debit, &l.credit); err != nil {
			t.Fatalf("scan: %v", err)
		}
		lines = append(lines, l)
	}

	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (DR 1500, CR 3110, CR 3120), got %d: %+v", len(lines), lines)
	}

	want := map[string]struct{ debit, credit float64 }{
		"1500": {3300, 0},
		"3110": {0, 800},
		"3120": {0, 2500},
	}
	for _, l := range lines {
		w, ok := want[l.code]
		if !ok {
			t.Errorf("unexpected account on entry: %+v", l)
			continue
		}
		if l.debit != w.debit || l.credit != w.credit {
			t.Errorf("account %s = DR %.2f CR %.2f, want DR %.2f CR %.2f", l.code, l.debit, l.credit, w.debit, w.credit)
		}
	}
}

// Single-category invoice keeps the same DR/CR shape as before
// (one CR line on the mapped category).
func TestSyncInvoicesSingleCategoryShape(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"treasurer"})
	svc := NewService(pool, nil, zerolog.Nop())
	if _, err := svc.SeedKontoplan(ctx, clubID); err != nil {
		t.Fatalf("seed kontoplan: %v", err)
	}

	var periodID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, 2026, '2026-01-01', '2026-12-31', 'open') RETURNING id`,
		clubID,
	).Scan(&periodID); err != nil {
		t.Fatalf("fiscal period: %v", err)
	}

	var duesItem string
	if err := pool.QueryRow(ctx,
		`INSERT INTO price_items (club_id, category, name, amount, unit, is_active)
		 VALUES ($1, 'dues', 'Medlemskontingent 2026', 500, 'year', true) RETURNING id`,
		clubID,
	).Scan(&duesItem); err != nil {
		t.Fatalf("dues item: %v", err)
	}

	var invoiceID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (club_id, user_id, invoice_number, issue_date, due_date, total_amount, status, kid_number)
		 VALUES ($1, $2, 1002, '2026-03-01', '2026-03-15', 500, 'sent', '1002') RETURNING id`,
		clubID, userID,
	).Scan(&invoiceID); err != nil {
		t.Fatalf("invoice: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total, price_item_id)
		 VALUES ($1, 'Medlemskontingent', 1, 500, 500, $2)`,
		invoiceID, duesItem,
	); err != nil {
		t.Fatalf("invoice line: %v", err)
	}

	if _, err := svc.SyncInvoices(ctx, clubID, periodID, userID); err != nil {
		t.Fatalf("SyncInvoices: %v", err)
	}

	var entryID string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM journal_entries WHERE club_id = $1 AND source = 'invoice_sync'`,
		clubID,
	).Scan(&entryID); err != nil {
		t.Fatalf("find entry: %v", err)
	}

	var nLines int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM journal_lines WHERE journal_entry_id = $1`, entryID,
	).Scan(&nLines); err != nil {
		t.Fatalf("count: %v", err)
	}
	if nLines != 2 {
		t.Errorf("expected 2 lines (DR 1500, CR 3100), got %d", nLines)
	}
}
