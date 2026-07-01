package accounting

import (
	"context"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/rs/zerolog"
)

// ReimportBankRows must replace only unmatched rows: a matched row (with a
// booked journal entry) survives untouched, its reappearance in the re-parsed
// file is skipped (no duplicate payment), and an unmatched row is replaced with
// its corrected parse.
func TestReimportPreservesMatchedRows(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"treasurer"})

	var importID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO bank_imports (club_id, filename, format, imported_by, row_count)
		 VALUES ($1, 't.csv', 'sparebank-norge-v1', $2, 0) RETURNING id`,
		clubID, userID,
	).Scan(&importID); err != nil {
		t.Fatalf("insert import: %v", err)
	}

	svc := NewService(pool, nil, zerolog.Nop())

	d1 := time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 6, 23, 0, 0, 0, 0, time.UTC)

	// Initial import: two unmatched rows.
	if _, err := svc.ImportBankRows(ctx, clubID, importID, "", []BankRow{
		{Date: d1, Description: "Nettgiro På Forfall", Amount: 450, Reference: "17626770001"},
		{Date: d2, Description: "Some other", Amount: 100, Reference: "AAA"},
	}); err != nil {
		t.Fatalf("initial import: %v", err)
	}

	// Simulate the first row being matched: give it a booked journal entry.
	var periodID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, 2026, '2026-01-01', '2026-12-31', 'open') RETURNING id`,
		clubID,
	).Scan(&periodID); err != nil {
		t.Fatalf("insert period: %v", err)
	}
	var jeID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO journal_entries (club_id, fiscal_period_id, entry_number, entry_date, description, created_by)
		 VALUES ($1, $2, 1, '2026-06-22', 'test', $3) RETURNING id`,
		clubID, periodID, userID,
	).Scan(&jeID); err != nil {
		t.Fatalf("insert journal entry: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE bank_import_rows SET journal_entry_id = $1
		  WHERE bank_import_id = $2 AND reference = '17626770001'`,
		jeID, importID,
	); err != nil {
		t.Fatalf("mark matched: %v", err)
	}

	// Re-import the corrected file: the booked row's transaction reappears (now
	// with the real KID parsed), plus the previously-unmatched row corrected.
	res, err := svc.ReimportBankRows(ctx, clubID, importID, "", []BankRow{
		{Date: d1, Description: "Fra: Trond Eivind Schei", Amount: 450, Reference: "17626770001", KID: "000000740019", Counterpart: "Trond Eivind Schei"},
		{Date: d2, Description: "Fra: Somebody", Amount: 100, Reference: "AAA", Counterpart: "Somebody"},
	})
	if err != nil {
		t.Fatalf("reimport: %v", err)
	}

	if res.Deleted != 1 {
		t.Errorf("Deleted = %d, want 1 (only the unmatched AAA row)", res.Deleted)
	}
	if res.SkippedBooked != 1 {
		t.Errorf("SkippedBooked = %d, want 1 (booked row's reappearance)", res.SkippedBooked)
	}

	// The matched row survives with its journal entry intact.
	var survived bool
	pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM bank_import_rows
		   WHERE bank_import_id = $1 AND reference = '17626770001' AND journal_entry_id = $2)`,
		importID, jeID,
	).Scan(&survived)
	if !survived {
		t.Error("matched row must survive re-import with its journal_entry_id intact")
	}

	// No duplicate of the booked payment — exactly one row with that reference.
	var refCount int
	pool.QueryRow(ctx,
		`SELECT count(*) FROM bank_import_rows WHERE bank_import_id = $1 AND reference = '17626770001'`,
		importID,
	).Scan(&refCount)
	if refCount != 1 {
		t.Errorf("booked-ref row count = %d, want 1 (no duplicate)", refCount)
	}

	// The unmatched row was replaced with its corrected parse (counterpart set).
	var aaaCount int
	var aaaCounterpart string
	pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(max(counterpart), '') FROM bank_import_rows
		  WHERE bank_import_id = $1 AND reference = 'AAA'`,
		importID,
	).Scan(&aaaCount, &aaaCounterpart)
	if aaaCount != 1 {
		t.Errorf("AAA row count = %d, want 1", aaaCount)
	}
	if aaaCounterpart != "Somebody" {
		t.Errorf("AAA counterpart = %q, want %q (re-parsed)", aaaCounterpart, "Somebody")
	}
}
