package accounting

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

type bankLinkFixture struct {
	svc  *Service
	club *fin.Club
}

func newBankLinkFixture(t *testing.T) *bankLinkFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, nil, zerolog.Nop())
	return &bankLinkFixture{svc: svc, club: fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))}
}

func (f *bankLinkFixture) kidInvoice(t *testing.T, kid string) fin.Invoice {
	t.Helper()
	return f.club.NewInvoice(t, fin.InvoiceOpts{KID: kid, Cents: 45000,
		Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
}

func (f *bankLinkFixture) kidRow(kid string) BankRow {
	return BankRow{
		Date:        time.Date(f.club.Year, 6, 14, 0, 0, 0, 0, time.UTC),
		Description: "Innbetaling KID " + kid,
		Amount:      450,
		Reference:   "SYN" + testutil.RandomHex(4),
		KID:         kid,
	}
}

// entryCounts returns the club's bank_import entries by status.
func (f *bankLinkFixture) entryCounts(t *testing.T) (posted, drafts int) {
	t.Helper()
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FILTER (WHERE status = 'posted'), count(*) FILTER (WHERE status = 'draft')
		   FROM journal_entries WHERE club_id = $1 AND source = 'bank_import'`,
		f.club.ID,
	).Scan(&posted, &drafts); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	return posted, drafts
}

// refusePosting makes every attempt to post a bank_import entry fail, the
// way a lost connection or a constraint would between link and post.
func (f *bankLinkFixture) refusePosting(t *testing.T) {
	t.Helper()
	if _, err := f.club.Pool.Exec(context.Background(), `
		CREATE FUNCTION refuse_bank_post() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'synthetic post failure'; END $$;
		CREATE TRIGGER refuse_bank_post BEFORE UPDATE ON journal_entries FOR EACH ROW
		  WHEN (OLD.source = 'bank_import' AND NEW.status = 'posted')
		  EXECUTE FUNCTION refuse_bank_post();`); err != nil {
		t.Fatalf("fault trigger: %v", err)
	}
}

// Two imports of the same statement at once book each payment once: the row
// hash lets only one insert through, and the guarded link lets only one
// entry claim the row.
func TestImportBankRowsConcurrentSameFile(t *testing.T) {
	f := newBankLinkFixture(t)
	ctx := context.Background()
	const kid = "000000881016"
	inv := f.kidInvoice(t, kid)
	rows := []BankRow{f.kidRow(kid)}
	imports := []string{f.club.NewBankImport(t), f.club.NewBankImport(t)}

	var wg sync.WaitGroup
	results := make([]ImportResult, len(imports))
	errs := make([]error, len(imports))
	for i, importID := range imports {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = f.svc.ImportBankRows(ctx, f.club.ID, importID, "", rows)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("import %d: %v", i, err)
		}
	}
	if got := results[0].Imported + results[1].Imported; got != 1 {
		t.Errorf("rows imported across both = %d, want 1", got)
	}
	if got := results[0].Matched + results[1].Matched; got != 1 {
		t.Errorf("KID matches across both = %d, want 1", got)
	}
	if posted, drafts := f.entryCounts(t); posted != 1 || drafts != 0 {
		t.Errorf("entries = %d posted, %d draft; want 1 posted, 0 draft", posted, drafts)
	}
	var payments int
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT count(*) FROM payments p JOIN invoices i ON i.payment_id = p.id WHERE i.id = $1`, inv.ID,
	).Scan(&payments); err != nil || payments != 1 {
		t.Errorf("faktura payments = %d (err=%v), want 1", payments, err)
	}
}

// When posting fails after the row was linked to its draft, the import
// unlinks the row and drops the draft: nothing is booked, the faktura stays
// owed, and the row is back in the queue for the next sync.
func TestImportBankRowsPostFailureAfterLinkUnwinds(t *testing.T) {
	f := newBankLinkFixture(t)
	ctx := context.Background()
	const kid = "000000881024"
	inv := f.kidInvoice(t, kid)
	f.refusePosting(t)

	importID := f.club.NewBankImport(t)
	res, err := f.svc.ImportBankRows(ctx, f.club.ID, importID, "", []BankRow{f.kidRow(kid)})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Matched != 0 || len(res.RowErrors) != 1 {
		t.Fatalf("import = matched %d, errors %+v; want 0 matched, 1 row error", res.Matched, res.RowErrors)
	}
	if posted, drafts := f.entryCounts(t); posted != 0 || drafts != 0 {
		t.Errorf("entries = %d posted, %d draft; want none", posted, drafts)
	}
	var linked, auto bool
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT journal_entry_id IS NOT NULL, auto_matched FROM bank_import_rows WHERE bank_import_id = $1`, importID,
	).Scan(&linked, &auto); err != nil {
		t.Fatalf("row: %v", err)
	}
	if linked || auto {
		t.Errorf("row linked=%v auto_matched=%v after a failed post; want both false", linked, auto)
	}
	var paid bool
	if err := f.club.Pool.QueryRow(ctx, `SELECT payment_id IS NOT NULL FROM invoices WHERE id = $1`, inv.ID).Scan(&paid); err != nil || paid {
		t.Errorf("faktura paid=%v (err=%v) after a failed post; want owed", paid, err)
	}
}

// A row a crashed KID match left linked to its unposted draft is released by
// bank sync and matched again. A fresh draft (an import still in flight), an
// intra-bank transfer draft (no receivable) and a mapping-rule draft that
// credits 1500 (AutoMatchImport; it waits for a gated post) are left alone.
func TestBankSyncReleasesStrandedKIDDraft(t *testing.T) {
	f := newBankLinkFixture(t)
	ctx := context.Background()
	const kid = "000000881037"
	inv := f.kidInvoice(t, kid)
	importID := f.club.NewBankImport(t)
	date := time.Date(f.club.Year, 6, 14, 0, 0, 0, 0, time.UTC)

	// draft links rowID to an unposted bank_import entry. kidShaped gives it
	// the shape the payment-match paths write (kidMatchDescription, sourced
	// to the row); otherwise it carries the row's own description, as a
	// mapping rule's draft does.
	draft := func(rowID, creditCode string, age time.Duration, kidShaped bool) string {
		t.Helper()
		var kidNumber, rowDesc string
		if err := f.club.Pool.QueryRow(ctx,
			`SELECT kid_number, description FROM bank_import_rows WHERE id = $1`, rowID,
		).Scan(&kidNumber, &rowDesc); err != nil {
			t.Fatalf("load row: %v", err)
		}
		desc := rowDesc
		if kidShaped {
			desc = kidMatchDescription(kidNumber, rowDesc)
		}
		sourceTable := "bank_import_rows"
		e, err := f.svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
			FiscalPeriodID: f.club.PeriodID,
			EntryDate:      date.Format("2006-01-02"),
			Description:    desc,
			Source:         "bank_import",
			SourceID:       &rowID,
			SourceTable:    &sourceTable,
			CreatedBy:      f.club.Treasurer.ID,
			ClubID:         f.club.ID,
			Lines: []CreateJournalLineInput{
				{AccountCode: bankAccountCode, Debit: 450},
				{AccountCode: creditCode, Credit: 450},
			},
		})
		if err != nil {
			t.Fatalf("draft: %v", err)
		}
		if _, err := f.club.Pool.Exec(ctx,
			`UPDATE journal_entries SET created_at = now() - $2::interval WHERE id = $1`,
			e.ID, age.String(),
		); err != nil {
			t.Fatalf("age draft: %v", err)
		}
		if _, err := f.club.Pool.Exec(ctx,
			`UPDATE bank_import_rows SET journal_entry_id = $1, auto_matched = true WHERE id = $2`,
			e.ID, rowID,
		); err != nil {
			t.Fatalf("link row: %v", err)
		}
		return e.ID
	}
	stranded := f.club.NewBankRow(t, importID, fin.BankRowOpts{Date: date, Cents: 45000, KID: kid})
	draft(stranded, receivablesAccountCode, time.Hour, true)
	inFlight := f.club.NewBankRow(t, importID, fin.BankRowOpts{Date: date, Cents: 45000})
	inFlightDraft := draft(inFlight, receivablesAccountCode, 0, true)
	transfer := f.club.NewBankRow(t, importID, fin.BankRowOpts{Date: date, Cents: 45000})
	transferDraft := draft(transfer, "1925", time.Hour, true)
	ruled := f.club.NewBankRow(t, importID, fin.BankRowOpts{Date: date, Cents: 45000})
	ruledDraft := draft(ruled, receivablesAccountCode, time.Hour, false)

	res, err := f.svc.BankSync(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("bank sync: %v", err)
	}
	if res.DraftsReleased != 1 || res.KIDMatched != 1 {
		t.Errorf("sync = released %d, KID matched %d; want 1 and 1", res.DraftsReleased, res.KIDMatched)
	}

	var status string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT je.status FROM bank_import_rows b JOIN journal_entries je ON je.id = b.journal_entry_id WHERE b.id = $1`,
		stranded,
	).Scan(&status); err != nil || status != "posted" {
		t.Errorf("stranded row now booked as %q (err=%v), want posted", status, err)
	}
	var paid bool
	if err := f.club.Pool.QueryRow(ctx, `SELECT payment_id IS NOT NULL FROM invoices WHERE id = $1`, inv.ID).Scan(&paid); err != nil || !paid {
		t.Errorf("faktura paid=%v (err=%v), want paid after the re-match", paid, err)
	}
	for row, entry := range map[string]string{inFlight: inFlightDraft, transfer: transferDraft, ruled: ruledDraft} {
		var linked string
		if err := f.club.Pool.QueryRow(ctx,
			`SELECT COALESCE(journal_entry_id::text, '') FROM bank_import_rows WHERE id = $1`, row,
		).Scan(&linked); err != nil || linked != entry {
			t.Errorf("row %s linked to %q (err=%v), want its draft %s left alone", row, linked, err, entry)
		}
	}
}
