package accounting

import (
	"context"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/rs/zerolog"
)

// The unmatched-row list must report, per incoming row, how many open unpaid
// invoices match its amount — the "worth opening?" hint. A row whose amount
// matches an open invoice reports a positive count; a row with no matching
// invoice reports zero (a genuinely orphan deposit).
func TestListUnmatchedBankRowsSuggestionCount(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
	svc := NewService(pool, nil, zerolog.Nop())

	var periodID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, 2026, '2026-01-01', '2026-12-31', 'open') RETURNING id`,
		clubID,
	).Scan(&periodID); err != nil {
		t.Fatalf("insert period: %v", err)
	}
	// One open, unpaid 450 invoice — the amount a bare row should hint at.
	if _, err := pool.Exec(ctx,
		`INSERT INTO invoices
		   (club_id, user_id, invoice_number, kid_number, due_date, total_amount,
		    status, fiscal_period_id, recipient_kind, recipient_email, sent_at)
		 VALUES ($1, $2, 500, '000005000018', '2026-06-14', 450.00,
		    'open', $3, 'private', 'm@example.com', now())`,
		clubID, userID, periodID,
	); err != nil {
		t.Fatalf("insert invoice: %v", err)
	}

	var importID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO bank_imports (club_id, filename, format, bank_account_code, imported_by, row_count)
		 VALUES ($1, 'x.csv', 'sparebank-norge-v1', '1920', $2, 0) RETURNING id`,
		clubID, userID,
	).Scan(&importID); err != nil {
		t.Fatalf("insert import: %v", err)
	}

	d := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	if _, err := svc.ImportBankRows(ctx, clubID, importID, "", []BankRow{
		{Date: d, Description: "Overførsel", Amount: 450, Reference: "R450"}, // matches the invoice amount
		{Date: d, Description: "Overførsel", Amount: 999, Reference: "R999"}, // nothing matches
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	rows, err := svc.ListUnmatchedBankRows(ctx, clubID, "incoming", "", 0, 50, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := map[string]int{}
	for _, r := range rows {
		got[r.Reference] = r.SuggestionCount
	}
	if got["R450"] != 1 {
		t.Errorf("R450 suggestion_count = %d, want 1 (the open 450 invoice)", got["R450"])
	}
	if got["R999"] != 0 {
		t.Errorf("R999 suggestion_count = %d, want 0 (no matching invoice)", got["R999"])
	}
}
