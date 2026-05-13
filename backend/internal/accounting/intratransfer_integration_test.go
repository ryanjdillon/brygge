package accounting

import (
	"context"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/rs/zerolog"
)

// Verifies the detector: a transfer between two of the club's bank accounts
// shows up on both statements; importing both must create exactly ONE journal
// entry (DR receiving, CR sending) and link both bank rows to it.
func TestDetectIntraBankTransfers(t *testing.T) {
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

	driftAccountNumber := "34700502793"
	hoyrenteAccountNumber := "36285182314"

	mkImport := func(name, accountCode, accountNumber string) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO bank_imports (club_id, filename, format, bank_account_code, bank_account_number, imported_by, row_count)
			 VALUES ($1, $2, 'sparebank-norge-v1', $3, $4, $5, 0) RETURNING id`,
			clubID, name, accountCode, accountNumber, userID,
		).Scan(&id); err != nil {
			t.Fatalf("insert bank_imports: %v", err)
		}
		return id
	}

	driftImport := mkImport("drift.csv", "1920", driftAccountNumber)
	hoyrenteImport := mkImport("hoyrente.csv", "1925", hoyrenteAccountNumber)

	driftRow := BankRow{
		Date:        time.Date(2026, 3, 25, 0, 0, 0, 0, time.UTC),
		Description: "Overføring Til Høyrentekonto",
		Amount:      -100000,
		Reference:   "T1",
		FromAccount: driftAccountNumber,
		ToAccount:   hoyrenteAccountNumber,
	}
	hoyRow := BankRow{
		Date:        time.Date(2026, 3, 25, 0, 0, 0, 0, time.UTC),
		Description: "Overføring Fra Driftskonto",
		Amount:      100000,
		Reference:   "T1-IN",
		FromAccount: driftAccountNumber,
		ToAccount:   hoyrenteAccountNumber,
	}

	if _, err := svc.ImportBankRows(ctx, clubID, driftImport, periodID, []BankRow{driftRow}); err != nil {
		t.Fatalf("import drift: %v", err)
	}

	res, err := svc.ImportBankRows(ctx, clubID, hoyrenteImport, periodID, []BankRow{hoyRow})
	if err != nil {
		t.Fatalf("import høyrente: %v", err)
	}
	if res.Transfers != 1 {
		t.Errorf("expected 1 transfer detected, got %d", res.Transfers)
	}

	var entryCount int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(DISTINCT journal_entry_id) FROM bank_import_rows
		 WHERE club_id = $1 AND journal_entry_id IS NOT NULL`,
		clubID,
	).Scan(&entryCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if entryCount != 1 {
		t.Errorf("expected 1 journal entry shared by both rows, got %d", entryCount)
	}

	var dr1920, cr1920, dr1925, cr1925 float64
	if err := pool.QueryRow(ctx,
		`SELECT
		   COALESCE(SUM(CASE WHEN a.code = '1920' THEN jl.debit END), 0),
		   COALESCE(SUM(CASE WHEN a.code = '1920' THEN jl.credit END), 0),
		   COALESCE(SUM(CASE WHEN a.code = '1925' THEN jl.debit END), 0),
		   COALESCE(SUM(CASE WHEN a.code = '1925' THEN jl.credit END), 0)
		 FROM journal_lines jl
		 JOIN journal_entries je ON je.id = jl.journal_entry_id
		 JOIN accounts a ON a.id = jl.account_id
		 WHERE je.club_id = $1`,
		clubID,
	).Scan(&dr1920, &cr1920, &dr1925, &cr1925); err != nil {
		t.Fatalf("sum lines: %v", err)
	}
	if cr1920 != 100000 || dr1920 != 0 {
		t.Errorf("1920 lines = DR %.2f CR %.2f, want DR 0 CR 100000", dr1920, cr1920)
	}
	if dr1925 != 100000 || cr1925 != 0 {
		t.Errorf("1925 lines = DR %.2f CR %.2f, want DR 100000 CR 0", dr1925, cr1925)
	}

	// Both bank rows must be linked to the same entry, marked auto_matched.
	var unmatched int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM bank_import_rows
		 WHERE club_id = $1 AND (journal_entry_id IS NULL OR NOT auto_matched)`,
		clubID,
	).Scan(&unmatched); err != nil {
		t.Fatalf("count unmatched: %v", err)
	}
	if unmatched != 0 {
		t.Errorf("expected both rows linked + auto_matched, %d still loose", unmatched)
	}
}
