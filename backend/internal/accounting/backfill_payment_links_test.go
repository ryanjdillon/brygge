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
		if err := svc.PostJournalEntry(ctx, clubID, entry.ID, userID); err != nil {
			t.Fatalf("post %s: %v", ref, err)
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

// backfillUnlinkedPayments keys on the KID, and an empty KID is no key:
// imported fakturas (empty KID) must not be marked paid by a booked bank row
// that also has no KID, however many share its amount. A faktura with a
// real KID still links.
func TestBackfillUnlinkedPaymentsLinksOnlyRealKIDPayments(t *testing.T) {
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

	// Imported rows mirror the uni24 import: no sent_at, and often no user.
	seedInv := func(number int, kid string, imported bool, member any) string {
		t.Helper()
		importSource, sentAt, externalID := any(nil), any(time.Now()), any(nil)
		if imported {
			importSource, sentAt, externalID = "uni24", nil, fmt.Sprintf("ext-%d", number)
		}
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO invoices
			   (club_id, user_id, invoice_number, kid_number, due_date, total_amount,
			    status, fiscal_period_id, recipient_kind, recipient_email, sent_at,
			    import_source, external_id)
			 VALUES ($1, $2, $3, $4, '2026-06-14', 620, 'open', $5, 'private', 'm@example.com', $6,
			         $7, $8)
			 RETURNING id`,
			clubID, member, number, kid, periodID, sentAt, importSource, externalID,
		).Scan(&id); err != nil {
			t.Fatalf("invoice %d: %v", number, err)
		}
		return id
	}
	imported1 := seedInv(9101, "", true, nil)
	imported2 := seedInv(9102, "", true, userID)
	withKID := seedInv(9103, "000000091034", false, userID)
	oldKID := seedInv(9104, "000000091045", false, userID)
	thisYear := seedInv(9105, "000000091056", false, userID)
	toRevenue := seedInv(9106, "000000091067", false, userID)
	draftOnly := seedInv(9107, "000000091078", false, userID)
	voided := seedInv(9108, "000000091089", false, userID)

	var importID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO bank_imports (club_id, filename, format, bank_account_code, imported_by, row_count)
		 VALUES ($1, 'b.csv', 'sparebank-norge-v1', '1920', $2, 0) RETURNING id`,
		clubID, userID,
	).Scan(&importID); err != nil {
		t.Fatalf("bank import: %v", err)
	}
	// Booked 620 kr deposits: one with no KID, one carrying withKID's, one
	// paid with oldKID's KID but assigned by hand to thisYear, and three whose
	// booking paid no receivable: to revenue, still a draft, and voided.
	for _, r := range []struct{ ref, kid, assigned, credit, state string }{
		{"NOKID", "", "", receivablesAccountCode, "posted"},
		{"KID", "000000091034", "", receivablesAccountCode, "posted"},
		{"OLDKID", "000000091045", thisYear, receivablesAccountCode, "posted"},
		{"REVENUE", "000000091067", "", "3900", "posted"},
		{"DRAFT", "000000091078", "", receivablesAccountCode, "draft"},
		{"VOIDED", "000000091089", "", receivablesAccountCode, "voided"},
	} {
		row := BankRow{Date: time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC), Description: "Innbetaling " + r.ref, Amount: 620, Reference: r.ref}
		entry, err := svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
			FiscalPeriodID: periodID, EntryDate: "2026-05-15", Description: "test",
			Source: "bank_import", ClubID: clubID, CreatedBy: userID,
			Lines: []CreateJournalLineInput{
				{AccountCode: "1920", Debit: 620},
				{AccountCode: r.credit, Credit: 620},
			},
		})
		if err != nil {
			t.Fatalf("journal %s: %v", r.ref, err)
		}
		if r.state != "draft" {
			if err := svc.PostJournalEntry(ctx, clubID, entry.ID, userID); err != nil {
				t.Fatalf("post %s: %v", r.ref, err)
			}
		}
		if r.state == "voided" {
			if _, err := svc.VoidJournalEntry(ctx, clubID, entry.ID, userID); err != nil {
				t.Fatalf("void %s: %v", r.ref, err)
			}
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO bank_import_rows
			   (bank_import_id, club_id, row_date, description, amount, reference, kid_number, row_hash,
			    journal_entry_id, invoice_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, '')::uuid)`,
			importID, clubID, row.Date, row.Description, row.Amount, row.Reference, r.kid, BankRowHash(clubID, row),
			entry.ID, r.assigned,
		); err != nil {
			t.Fatalf("insert row %s: %v", r.ref, err)
		}
	}

	n, err := svc.backfillUnlinkedPayments(ctx, clubID)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 1 {
		t.Errorf("linked %d, want 1 (only the faktura with a real KID and an unassigned row)", n)
	}
	for _, c := range []struct {
		name string
		id   string
		want bool
	}{
		{"imported, no member", imported1, false},
		{"imported, member", imported2, false},
		{"with KID", withKID, true},
		{"old KID on a row assigned elsewhere", oldKID, false},
		{"KID row booked to revenue", toRevenue, false},
		{"KID row on a draft entry", draftOnly, false},
		{"KID row on a voided entry", voided, false},
	} {
		var paid bool
		if err := pool.QueryRow(ctx, `SELECT payment_id IS NOT NULL FROM invoices WHERE id=$1`, c.id).Scan(&paid); err != nil {
			t.Fatalf("read %s: %v", c.name, err)
		}
		if paid != c.want {
			t.Errorf("%s: paid=%v, want %v", c.name, paid, c.want)
		}
	}
}
