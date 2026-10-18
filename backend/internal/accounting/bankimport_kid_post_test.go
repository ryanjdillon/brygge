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

// A rejected-KID notice ("ble ikke akseptert": the money never arrived) is
// stored but never matched, at import as at sync, even with a KID that
// matches an open faktura.
func TestImportBankRowsSkipsRejectedKID(t *testing.T) {
	f := newBankLinkFixture(t)
	const kid = "000000881086"
	inv := f.kidInvoice(t, kid)
	row := f.kidRow(kid)
	row.Description = "KID " + kid + " ble ikke akseptert på denne kontoen"

	importID := f.club.NewBankImport(t)
	res, err := f.svc.ImportBankRows(context.Background(), f.club.ID, importID, "", []BankRow{row})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Imported != 1 || res.Matched != 0 || len(res.RowErrors) != 0 {
		t.Errorf("import = imported %d, matched %d, errors %+v; want the row stored, unmatched, no errors",
			res.Imported, res.Matched, res.RowErrors)
	}
	if sync := f.sync(t); sync.KIDMatched != 0 {
		t.Errorf("sync KID matched = %d, want 0", sync.KIDMatched)
	}
	if posted, drafts := f.entryCounts(t); posted != 0 || drafts != 0 {
		t.Errorf("entries = %d posted, %d draft; want none", posted, drafts)
	}
	if f.invoicePaid(t, inv.ID) {
		t.Errorf("faktura marked paid by a rejected payment")
	}
}

// An import's KID match and a Vipps charge assign race for one faktura:
// exactly one books it. A losing KID row is left in Tildel and reported.
func TestImportBankRowsKIDRacesVippsAssign(t *testing.T) {
	f := newBankLinkFixture(t)
	ctx := context.Background()
	const kid = "000000881094"
	inv := f.kidInvoice(t, kid)
	vippsRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payer"})
	importID := f.club.NewBankImport(t)

	var res ImportResult
	var ierr, verr error
	contend(t, f.club, inv.ID,
		func() { res, ierr = f.svc.ImportBankRows(ctx, f.club.ID, importID, "", []BankRow{f.kidRow(kid)}) },
		func() {
			_, verr = f.svc.AssignVippsChargeToInvoice(ctx, f.club.ID, vippsRow, inv.ID, f.club.Treasurer.ID)
		},
	)
	if ierr != nil {
		t.Fatalf("import: %v", ierr)
	}

	if n := countPosted(t, f.club, "bank_import_rows") + countPosted(t, f.club, "vipps_import_rows"); n != 1 {
		t.Fatalf("posted entries = %d, want exactly one", n)
	}
	var bankRow string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT id FROM bank_import_rows WHERE bank_import_id = $1`, importID).Scan(&bankRow); err != nil {
		t.Fatalf("bank row: %v", err)
	}
	bankEntry, _, bankInvoice := f.cascadeRow(t, bankRow)
	vippsEntry := rowEntry(t, f.club, vippsRow)
	switch {
	case bankEntry != "" && vippsEntry == "":
		if verr == nil {
			t.Errorf("Vipps assign succeeded without booking")
		}
		if res.Matched != 1 || bankInvoice != inv.ID {
			t.Errorf("KID won: matched %d, recorded faktura %q; want 1 and %s", res.Matched, bankInvoice, inv.ID)
		}
	case vippsEntry != "" && bankEntry == "":
		if verr != nil {
			t.Errorf("Vipps assign: %v", verr)
		}
		if res.Matched != 0 || res.KIDSkippedSettled != 1 || len(res.RowErrors) != 1 {
			t.Errorf("Vipps won: matched %d, skipped settled %d, errors %+v; want 0, 1 and one row error",
				res.Matched, res.KIDSkippedSettled, res.RowErrors)
		}
	default:
		t.Errorf("bank row booked %q, Vipps row booked %q; want exactly one", bankEntry, vippsEntry)
	}
	if !f.invoicePaid(t, inv.ID) {
		t.Errorf("faktura not marked paid")
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
	if err := svc.PostJournalEntry(ctx, clubID, posted, userID); err != nil {
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
