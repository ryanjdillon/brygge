package accounting

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// A multi-category invoice should produce a balanced bilag with one
// DR line on 1500 receivables and one CR line per price-item category.
func TestSyncInvoicesSplitsRevenuePerCategory(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	svc := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	clubID, userID, periodID := club.ID, club.Treasurer.ID, club.PeriodID

	// Two lines in different categories so the resulting bilag must split
	// CR across two revenue accounts.
	club.NewInvoice(t, fin.InvoiceOpts{
		Number: 1001,
		Lines: []fin.Line{
			{Category: "harbor_membership", Cents: 80000},
			{Category: "slip_fee", Cents: 250000},
		},
	})

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
		code          string
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

	svc := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	clubID, userID, periodID := club.ID, club.Treasurer.ID, club.PeriodID

	club.NewInvoice(t, fin.InvoiceOpts{
		Number: 1002,
		Lines:  []fin.Line{{Category: "dues", Cents: 50000}},
	})

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
