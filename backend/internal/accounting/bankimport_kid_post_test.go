package accounting

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// An import-time KID match must leave exactly one posted entry linked to the
// row and settle the invoice. Before BRY-324 it passed the club id as
// posted_by, the FK failed silently, and every match left an orphan draft
// that a later bank sync booked a second time.
func TestImportBankRowsKIDMatchPostsAndLinks(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	svc := NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	clubID := club.ID

	const kid = "000000777013"
	invoiceID := club.NewInvoice(t, fin.InvoiceOpts{
		Number: 7701, KID: kid, Cents: 45000,
		Lines: []fin.Line{{Category: "membership", Cents: 45000}},
	}).ID
	importID := club.NewBankImport(t)

	rows := []BankRow{{
		Date:        time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC),
		Description: "Innbetaling KID " + kid,
		Amount:      450,
		Reference:   "SYN" + testutil.RandomHex(4),
		KID:         kid,
	}}

	res, err := svc.ImportBankRows(ctx, clubID, importID, "", rows)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Matched != 1 || len(res.RowErrors) != 0 {
		t.Fatalf("import result = matched %d, errors %+v; want 1 match, no errors", res.Matched, res.RowErrors)
	}

	var posted, drafts int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE status = 'posted'), count(*) FILTER (WHERE status = 'draft')
		   FROM journal_entries WHERE club_id = $1 AND source = 'bank_import'`,
		clubID,
	).Scan(&posted, &drafts); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	if posted != 1 || drafts != 0 {
		t.Errorf("entries = %d posted, %d draft; want 1 posted, 0 draft", posted, drafts)
	}

	var linkedPosted bool
	if err := pool.QueryRow(ctx,
		`SELECT je.status = 'posted'
		   FROM bank_import_rows bir JOIN journal_entries je ON je.id = bir.journal_entry_id
		  WHERE bir.bank_import_id = $1`,
		importID,
	).Scan(&linkedPosted); err != nil || !linkedPosted {
		t.Errorf("row not linked to a posted entry (posted=%v, err=%v)", linkedPosted, err)
	}

	var settled bool
	if err := pool.QueryRow(ctx,
		`SELECT payment_id IS NOT NULL FROM invoices WHERE id = $1`, invoiceID,
	).Scan(&settled); err != nil || !settled {
		t.Errorf("invoice payment_id not set (settled=%v, err=%v)", settled, err)
	}

	// Re-importing the same file is a no-op: no new rows, no new entries.
	res2, err := svc.ImportBankRows(ctx, clubID, importID, "", rows)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if res2.Imported != 0 || res2.Matched != 0 {
		t.Errorf("re-import = imported %d, matched %d; want 0/0", res2.Imported, res2.Matched)
	}
	var total int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM journal_entries WHERE club_id = $1 AND source = 'bank_import'`, clubID,
	).Scan(&total); err != nil || total != 1 {
		t.Errorf("entries after re-import = %d (err=%v), want 1", total, err)
	}
}

func TestDiscardDraftLeavesPostedEntries(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"treasurer"})
	svc := NewService(pool, nil, zerolog.Nop())
	if _, err := svc.SeedKontoplan(ctx, clubID); err != nil {
		t.Fatalf("seed kontoplan: %v", err)
	}
	periodID, _, err := svc.resolvePeriod(ctx, clubID, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), "")
	if err != nil {
		t.Fatalf("period: %v", err)
	}

	mk := func() string {
		e, err := svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
			FiscalPeriodID: periodID,
			EntryDate:      "2026-06-01",
			Description:    "Synthetic",
			Source:         "bank_import",
			CreatedBy:      userID,
			ClubID:         clubID,
			Lines: []CreateJournalLineInput{
				{AccountCode: bankAccountCode, Debit: 100},
				{AccountCode: receivablesAccountCode, Credit: 100},
			},
		})
		if err != nil {
			t.Fatalf("create entry: %v", err)
		}
		return e.ID
	}
	draft := mk()
	posted := mk()
	if err := svc.PostJournalEntry(ctx, posted, userID); err != nil {
		t.Fatalf("post: %v", err)
	}

	svc.discardDraft(ctx, clubID, draft)
	svc.discardDraft(ctx, clubID, posted)

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM journal_entries WHERE id = $1`, draft).Scan(&n); err != nil || n != 0 {
		t.Errorf("draft still present (n=%d, err=%v)", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM journal_entries WHERE id = $1`, posted).Scan(&n); err != nil || n != 1 {
		t.Errorf("posted entry was removed (n=%d, err=%v)", n, err)
	}
}
