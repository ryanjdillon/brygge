package accounting

import (
	"context"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/rs/zerolog"
)

// BackfillCounterpartEncoding must re-decode legacy-encoded counterparts on all
// rows — including booked ones (which a re-import never re-parses) — leave clean
// names alone, and be idempotent.
func TestBackfillCounterpartEncoding(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"treasurer"})
	svc := NewService(pool, nil, zerolog.Nop())

	var importID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO bank_imports (club_id, filename, format, bank_account_code, imported_by, row_count)
		 VALUES ($1, 'x.csv', 'sparebank-norge-v1', '1920', $2, 0) RETURNING id`,
		clubID, userID,
	).Scan(&importID); err != nil {
		t.Fatalf("insert import: %v", err)
	}

	d := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	// Two garbled names (one will be booked) + one clean name that must be left.
	if _, err := svc.ImportBankRows(ctx, clubID, importID, "", []BankRow{
		{Date: d, Description: "Overførsel", Amount: 450, Reference: "G1", Counterpart: "Arvid Johan Lok@Y"},
		{Date: d, Description: "Overførsel", Amount: 300, Reference: "G2", Counterpart: "Klokkarvik B}Tlag"},
		{Date: d, Description: "Overførsel", Amount: 100, Reference: "C1", Counterpart: "Ola Nordmann"},
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Book the first row so we prove booked rows are covered by the backfill.
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
		 VALUES ($1, $2, 1, '2026-06-04', 'test', $3) RETURNING id`,
		clubID, periodID, userID,
	).Scan(&jeID); err != nil {
		t.Fatalf("insert journal entry: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE bank_import_rows SET journal_entry_id = $1 WHERE bank_import_id = $2 AND reference = 'G1'`,
		jeID, importID,
	); err != nil {
		t.Fatalf("book row: %v", err)
	}

	// Dry run: counts the two garbled rows, writes nothing.
	dry, err := BackfillCounterpartEncoding(ctx, pool, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.Scanned != 2 || dry.Changed != 2 {
		t.Errorf("dry run scanned=%d changed=%d, want 2/2", dry.Scanned, dry.Changed)
	}
	var stillGarbled string
	pool.QueryRow(ctx, `SELECT counterpart FROM bank_import_rows WHERE reference = 'G1'`).Scan(&stillGarbled)
	if stillGarbled != "Arvid Johan Lok@Y" {
		t.Errorf("dry run must not write: G1 counterpart = %q", stillGarbled)
	}

	// Real run: rewrites both, including the booked row.
	got, err := BackfillCounterpartEncoding(ctx, pool, false)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if got.Changed != 2 {
		t.Errorf("backfill changed=%d, want 2", got.Changed)
	}

	check := func(ref, want string) {
		var cp string
		pool.QueryRow(ctx, `SELECT counterpart FROM bank_import_rows WHERE reference = $1`, ref).Scan(&cp)
		if cp != want {
			t.Errorf("%s counterpart = %q, want %q", ref, cp, want)
		}
	}
	check("G1", "Arvid Johan Lokøy") // booked row was decoded
	check("G2", "Klokkarvik Båtlag")
	check("C1", "Ola Nordmann") // clean name untouched

	// Idempotent: a second pass finds nothing.
	again, err := BackfillCounterpartEncoding(ctx, pool, false)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if again.Scanned != 0 || again.Changed != 0 {
		t.Errorf("second pass scanned=%d changed=%d, want 0/0", again.Scanned, again.Changed)
	}
}
