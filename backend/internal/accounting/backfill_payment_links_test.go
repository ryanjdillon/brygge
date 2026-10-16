package accounting

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/rs/zerolog"
)

// backfillInvoiceNumberPayments links payment for an open invoice whose matched
// bank row references it by "Faktura NN" and credits receivables, but skips a
// row that credits a revenue account (a Lag-bilag booking, not an AR payment).
func TestBackfillInvoiceNumberPayments(t *testing.T) {
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
		t.Fatalf("period: %v", err)
	}

	// inv77: paid by "Faktura 77" → receivables credit (should link).
	// inv88: matched row credits revenue 3900 (should NOT link).
	seedInv := func(number int, amount float64) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO invoices
			   (club_id, user_id, invoice_number, kid_number, due_date, total_amount,
			    status, fiscal_period_id, recipient_kind, recipient_email, sent_at)
			 VALUES ($1, $2, $3, $4, '2026-06-14', $5, 'open', $6, 'private', 'm@example.com', now())
			 RETURNING id`,
			clubID, userID, number, fmt.Sprintf("%012d", number), amount, periodID,
		).Scan(&id); err != nil {
			t.Fatalf("invoice %d: %v", number, err)
		}
		return id
	}
	inv77 := seedInv(77, 450)
	inv88 := seedInv(88, 300)

	var importID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO bank_imports (club_id, filename, format, bank_account_code, imported_by, row_count)
		 VALUES ($1, 'b.csv', 'sparebank-norge-v1', '1920', $2, 0) RETURNING id`,
		clubID, userID,
	).Scan(&importID); err != nil {
		t.Fatalf("bank import: %v", err)
	}
	// Insert the rows directly rather than through ImportBankRows: the
	// importer would auto-match "Faktura NN" itself, and this test needs the
	// rows booked by hand below to exercise only the backfill.
	for _, r := range []BankRow{
		{Date: time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC), Description: "Mobilbank · Faktura 77", Amount: 450, Reference: "R77"},
		{Date: time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC), Description: "Mobilbank · Faktura 88", Amount: 300, Reference: "R88"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO bank_import_rows (bank_import_id, club_id, row_date, description, amount, reference, row_hash)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			importID, clubID, r.Date, r.Description, r.Amount, r.Reference, BankRowHash(clubID, r),
		); err != nil {
			t.Fatalf("insert row %s: %v", r.Reference, err)
		}
	}

	// Book each row: inv77 as a real AR payment (CR 1500), inv88 to revenue.
	link := func(ref, creditAccount string, amount float64) {
		t.Helper()
		var rowID string
		if err := pool.QueryRow(ctx,
			`SELECT id FROM bank_import_rows WHERE club_id=$1 AND reference=$2`, clubID, ref,
		).Scan(&rowID); err != nil {
			t.Fatalf("row %s: %v", ref, err)
		}
		entry, err := svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
			FiscalPeriodID: periodID, EntryDate: "2026-06-03", Description: "test",
			Source: "bank_import", ClubID: clubID, CreatedBy: userID,
			Lines: []CreateJournalLineInput{
				{AccountCode: "1920", Debit: amount},
				{AccountCode: creditAccount, Credit: amount},
			},
		})
		if err != nil {
			t.Fatalf("journal %s: %v", ref, err)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE bank_import_rows SET journal_entry_id=$1 WHERE id=$2`, entry.ID, rowID,
		); err != nil {
			t.Fatalf("mark %s: %v", ref, err)
		}
	}
	link("R77", receivablesAccountCode, 450)
	link("R88", "3900", 300)

	n, err := svc.backfillInvoiceNumberPayments(ctx, clubID)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 1 {
		t.Errorf("linked %d, want 1 (only inv77; inv88 credits revenue)", n)
	}

	assertPaid := func(id string, want bool) {
		t.Helper()
		var paid bool
		if err := pool.QueryRow(ctx,
			`SELECT payment_id IS NOT NULL FROM invoices WHERE id=$1`, id,
		).Scan(&paid); err != nil {
			t.Fatalf("read invoice: %v", err)
		}
		if paid != want {
			t.Errorf("invoice paid=%v, want %v", paid, want)
		}
	}
	assertPaid(inv77, true)
	assertPaid(inv88, false)

	// Idempotent: a second run links nothing new.
	if n2, err := svc.backfillInvoiceNumberPayments(ctx, clubID); err != nil || n2 != 0 {
		t.Errorf("second run = %d (err %v), want 0", n2, err)
	}
}
