package accounting

import (
	"context"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/rs/zerolog"
)

// ListMissingVippsSettlements flags unmatched Vipps payout bank rows whose
// settlement isn't imported, and ignores payouts whose settlement IS imported,
// non-Vipps rows, and already-matched rows.
func TestListMissingVippsSettlements(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"treasurer"})
	svc := NewService(pool, nil, zerolog.Nop())

	var importID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO bank_imports (club_id, filename, format, bank_account_code, imported_by, row_count)
		 VALUES ($1, 'b.csv', 'sparebank-norge-v1', '1920', $2, 0) RETURNING id`,
		clubID, userID,
	).Scan(&importID); err != nil {
		t.Fatalf("insert import: %v", err)
	}

	d := time.Date(2026, 3, 25, 0, 0, 0, 0, time.UTC)
	if _, err := svc.ImportBankRows(ctx, clubID, importID, "", []BankRow{
		{Date: d, Description: "Utb. 3000100 Vippsnr 548005", Amount: 500, Reference: "A1"}, // settlement NOT imported
		{Date: d, Description: "Utb. 3000200 Vippsnr 548005", Amount: 700, Reference: "A2"}, // settlement imported
		{Date: d, Description: "Nettgiro overførsel", Amount: 300, Reference: "A3"},         // not a Vipps payout
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Import settlement 3000200 (so it is NOT missing).
	var vippsImportID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO vipps_imports (club_id, filename, msn, imported_by)
		 VALUES ($1, 's.csv', '548005', $2) RETURNING id`,
		clubID, userID,
	).Scan(&vippsImportID); err != nil {
		t.Fatalf("insert vipps import: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO vipps_import_rows
		   (vipps_import_id, club_id, row_hash, row_type, amount, settlement_number, msn)
		 VALUES ($1, $2, repeat('a',64), 'belastning', 700.00, '3000200', '548005')`,
		vippsImportID, clubID,
	); err != nil {
		t.Fatalf("insert vipps row: %v", err)
	}

	missing, err := svc.ListMissingVippsSettlements(ctx, clubID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(missing) != 1 {
		t.Fatalf("got %d missing, want 1 (only 3000100)", len(missing))
	}
	m := missing[0]
	if m.SettlementNumber != "3000100" || m.MSN != "548005" || m.Amount != 500 {
		t.Errorf("missing = %+v, want settlement 3000100 / msn 548005 / amount 500", m)
	}
}

// ListUnreconciledVippsCharges surfaces one item per un-journaled belastning
// row (the per-payer Tildel lines), with the amount-matched invoice suggestion
// count, and excludes already-reconciled charges and non-belastning rows.
func TestListUnreconciledVippsCharges(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"treasurer"})
	svc := NewService(pool, nil, zerolog.Nop())

	// One open, unpaid 450 invoice → the 450 charge should hint at it.
	var periodID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, 2026, '2026-01-01', '2026-12-31', 'open') RETURNING id`,
		clubID,
	).Scan(&periodID); err != nil {
		t.Fatalf("insert period: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO invoices
		   (club_id, user_id, invoice_number, kid_number, due_date, total_amount,
		    status, fiscal_period_id, recipient_kind, recipient_email, sent_at)
		 VALUES ($1, $2, 700, '000007000015', '2026-06-14', 450.00,
		    'open', $3, 'private', 'm@example.com', now())`,
		clubID, userID, periodID,
	); err != nil {
		t.Fatalf("insert invoice: %v", err)
	}

	// A journal entry to mark one charge as already reconciled.
	var jeID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO journal_entries (club_id, fiscal_period_id, entry_number, entry_date, description, created_by)
		 VALUES ($1, $2, 1, '2026-06-04', 'test', $3) RETURNING id`,
		clubID, periodID, userID,
	).Scan(&jeID); err != nil {
		t.Fatalf("insert journal entry: %v", err)
	}

	var importID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO vipps_imports (club_id, filename, msn, imported_by)
		 VALUES ($1, 'settlement.csv', '548005', $2) RETURNING id`,
		clubID, userID,
	).Scan(&importID); err != nil {
		t.Fatalf("insert vipps import: %v", err)
	}

	// A=charge matching the invoice amount; B=charge with no match; C=charge
	// already reconciled (journal_entry_id set); F=a fee row (not a charge).
	if _, err := pool.Exec(ctx,
		`INSERT INTO vipps_import_rows
		   (vipps_import_id, club_id, row_hash, row_type, booking_date, amount,
		    customer_name, message, settlement_number, msn, journal_entry_id)
		 VALUES
		   ($1, $2, repeat('a',64), 'belastning', '2026-06-04', 450.00,
		    'Ola Nordmann', 'Sesong 2026', '2000167', '548005', NULL),
		   ($1, $2, repeat('b',64), 'belastning', '2026-06-04', 999.00,
		    'Kari Testperson', '', '2000167', '548005', NULL),
		   ($1, $2, repeat('c',64), 'belastning', '2026-06-04', 450.00,
		    'Per Reconciled', '', '2000167', '548005', $3),
		   ($1, $2, repeat('f',64), 'fee', '2026-06-04', 12.50,
		    '', '', '2000167', '548005', NULL)`,
		importID, clubID, jeID,
	); err != nil {
		t.Fatalf("insert vipps rows: %v", err)
	}

	items, err := svc.ListUnreconciledVippsCharges(ctx, clubID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2 (A + B; C reconciled and F fee excluded)", len(items))
	}

	byName := map[string]ReconcileItem{}
	for _, it := range items {
		if it.Source != "vipps" {
			t.Errorf("item %s source = %q, want vipps", it.ID, it.Source)
		}
		byName[it.Counterpart] = it
	}

	a, ok := byName["Ola Nordmann"]
	if !ok {
		t.Fatal("charge A (Ola Nordmann) missing")
	}
	if a.SuggestionCount != 1 {
		t.Errorf("A suggestion_count = %d, want 1 (the open 450 invoice)", a.SuggestionCount)
	}
	if a.Amount != 450 || a.Reference != "2000167" || a.Description != "Sesong 2026" {
		t.Errorf("A mapped wrong: amount=%v ref=%q desc=%q", a.Amount, a.Reference, a.Description)
	}

	b, ok := byName["Kari Testperson"]
	if !ok {
		t.Fatal("charge B (Kari Testperson) missing")
	}
	if b.SuggestionCount != 0 {
		t.Errorf("B suggestion_count = %d, want 0 (no matching invoice)", b.SuggestionCount)
	}

	if _, present := byName["Per Reconciled"]; present {
		t.Error("reconciled charge C must not appear")
	}
}
