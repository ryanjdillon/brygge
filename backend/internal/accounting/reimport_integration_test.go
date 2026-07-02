package accounting

import (
	"context"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/rs/zerolog"
)

// ReimportAccountBankRows must clear the account's unmatched rows ONLY within
// the re-import CSV's date range: an unmatched row outside that window survives,
// a booked row inside the window survives (with its bilag) and its reappearance
// is skipped, and an unmatched ghost inside the window is replaced by the
// corrected parse.
func TestReimportAccountScopedToDateRange(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"treasurer"})
	svc := NewService(pool, nil, zerolog.Nop())

	const acct = "1920"
	var import1 string
	if err := pool.QueryRow(ctx,
		`INSERT INTO bank_imports (club_id, filename, format, bank_account_code, imported_by, row_count)
		 VALUES ($1, 'jan.csv', 'sparebank-norge-v1', $2, $3, 0) RETURNING id`,
		clubID, acct, userID,
	).Scan(&import1); err != nil {
		t.Fatalf("insert import1: %v", err)
	}

	dOut := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)     // before the re-import window
	dBooked := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC) // inside, gets booked
	dGhost := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)  // inside, unmatched ghost

	if _, err := svc.ImportBankRows(ctx, clubID, import1, "", []BankRow{
		{Date: dOut, Description: "old unmatched", Amount: 111, Reference: "OUT1"},
		{Date: dBooked, Description: "to be booked", Amount: 222, Reference: "BOOK1"},
		{Date: dGhost, Description: "ghost misparse", Amount: 333, Reference: "GHOST1"},
	}); err != nil {
		t.Fatalf("initial import: %v", err)
	}

	// Book the middle row.
	var periodID, jeID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, 2026, '2026-01-01', '2026-12-31', 'open') RETURNING id`,
		clubID,
	).Scan(&periodID); err != nil {
		t.Fatalf("insert period: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO journal_entries (club_id, fiscal_period_id, entry_number, entry_date, description, created_by)
		 VALUES ($1, $2, 1, '2026-06-10', 'test', $3) RETURNING id`,
		clubID, periodID, userID,
	).Scan(&jeID); err != nil {
		t.Fatalf("insert journal entry: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE bank_import_rows SET journal_entry_id = $1 WHERE bank_import_id = $2 AND reference = 'BOOK1'`,
		jeID, import1,
	); err != nil {
		t.Fatalf("mark booked: %v", err)
	}

	// Re-import a corrected CSV covering [dBooked, dGhost] into a new import.
	var import2 string
	if err := pool.QueryRow(ctx,
		`INSERT INTO bank_imports (club_id, filename, format, bank_account_code, imported_by, row_count)
		 VALUES ($1, 'full.csv', 'sparebank-norge-v1', $2, $3, 0) RETURNING id`,
		clubID, acct, userID,
	).Scan(&import2); err != nil {
		t.Fatalf("insert import2: %v", err)
	}

	res, err := svc.ReimportAccountBankRows(ctx, clubID, acct, import2, "", []BankRow{
		{Date: dBooked, Description: "booked reappears", Amount: 222, Reference: "BOOK1", Counterpart: "Someone"},
		{Date: dGhost, Description: "ghost corrected", Amount: 333, Reference: "GHOST1", Counterpart: "Corrected"},
	})
	if err != nil {
		t.Fatalf("account reimport: %v", err)
	}

	if res.Deleted != 1 {
		t.Errorf("Deleted = %d, want 1 (only the in-range unmatched GHOST1)", res.Deleted)
	}
	if res.SkippedBooked != 1 {
		t.Errorf("SkippedBooked = %d, want 1 (BOOK1 reappearance)", res.SkippedBooked)
	}

	// Out-of-range unmatched row survives untouched.
	var outExists bool
	pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM bank_import_rows WHERE bank_import_id = $1 AND reference = 'OUT1')`,
		import1,
	).Scan(&outExists)
	if !outExists {
		t.Error("out-of-range row OUT1 must survive the scoped re-import")
	}

	// Booked row survives with its journal entry, exactly one copy.
	var bookCount int
	var bookHasJE bool
	pool.QueryRow(ctx,
		`SELECT count(*), bool_and(journal_entry_id IS NOT NULL)
		   FROM bank_import_rows WHERE club_id = $1 AND reference = 'BOOK1'`,
		clubID,
	).Scan(&bookCount, &bookHasJE)
	if bookCount != 1 || !bookHasJE {
		t.Errorf("BOOK1: count=%d hasJE=%v, want 1 booked row (no duplicate)", bookCount, bookHasJE)
	}

	// Ghost replaced by the corrected parse: exactly one GHOST1, now in import2.
	var ghostCount int
	var ghostImport, ghostCounterpart string
	pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(max(bank_import_id::text), ''), COALESCE(max(counterpart), '')
		   FROM bank_import_rows WHERE club_id = $1 AND reference = 'GHOST1'`,
		clubID,
	).Scan(&ghostCount, &ghostImport, &ghostCounterpart)
	if ghostCount != 1 {
		t.Errorf("GHOST1 count = %d, want 1 (old ghost replaced, not duplicated)", ghostCount)
	}
	if ghostImport != import2 {
		t.Errorf("GHOST1 now in import %s, want the new import %s", ghostImport, import2)
	}
	if ghostCounterpart != "Corrected" {
		t.Errorf("GHOST1 counterpart = %q, want %q (re-parsed)", ghostCounterpart, "Corrected")
	}
}

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
