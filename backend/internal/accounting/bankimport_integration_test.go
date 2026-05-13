package accounting

import (
	"context"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/rs/zerolog"
)

// Verifies the (club_id, row_hash) unique constraint and ON CONFLICT path:
// a second import of the same rows reports them as skipped duplicates.
func TestImportBankRowsDeduplicates(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"treasurer"})

	var importID string
	err := pool.QueryRow(ctx,
		`INSERT INTO bank_imports (club_id, filename, format, imported_by, row_count)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		clubID, "test.csv", "sparebank-norge-v1", userID, 0,
	).Scan(&importID)
	if err != nil {
		t.Fatalf("inserting bank_imports: %v", err)
	}

	svc := NewService(pool, nil, zerolog.Nop())

	rows := []BankRow{
		{
			Date:        time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
			Description: "Overførsel",
			Amount:      3281.55,
			Reference:   "273196130",
		},
		{
			Date:        time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
			Description: "Utb. 2000591 Vippsnr 698382",
			Amount:      1572,
			Reference:   "272437290",
			Counterpart: "Vipps Mobilepay As",
		},
	}

	res1, err := svc.ImportBankRows(ctx, clubID, importID, "", rows)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if res1.Imported != 2 || res1.SkippedDup != 0 {
		t.Fatalf("first import counts: imported=%d skipped=%d, want 2/0", res1.Imported, res1.SkippedDup)
	}

	res2, err := svc.ImportBankRows(ctx, clubID, importID, "", rows)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if res2.Imported != 0 || res2.SkippedDup != 2 {
		t.Fatalf("second import counts: imported=%d skipped=%d, want 0/2", res2.Imported, res2.SkippedDup)
	}

	// A new row with a different amount should still go through.
	rows = append(rows, BankRow{
		Date:        time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC),
		Description: "Ny rad",
		Amount:      99.99,
		Reference:   "X1",
	})
	res3, err := svc.ImportBankRows(ctx, clubID, importID, "", rows)
	if err != nil {
		t.Fatalf("third import: %v", err)
	}
	if res3.Imported != 1 || res3.SkippedDup != 2 {
		t.Fatalf("third import counts: imported=%d skipped=%d, want 1/2", res3.Imported, res3.SkippedDup)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM bank_import_rows WHERE club_id = $1`, clubID,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 stored rows, got %d", n)
	}
}
