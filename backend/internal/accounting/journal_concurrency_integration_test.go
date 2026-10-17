package accounting

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// Two operators assigning the same bank row to fakturas at once: exactly
// one booking lands and the row points at it. The loser's draft is
// discarded, so no posted entry exists without its row.
func TestAssignBankRowToInvoiceConcurrentOneWins(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()
	row := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 45000})
	fakturas := []fin.Invoice{
		f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}}),
		f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}}),
	}

	errs := make([]error, len(fakturas))
	var wg sync.WaitGroup
	for i, inv := range fakturas {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.svc.AssignBankRowToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, row, inv.ID)
		}()
	}
	wg.Wait()

	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, errBankRowRaced):
		default:
			t.Errorf("assign: unexpected error %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d assigns succeeded, want exactly 1 (%v)", ok, errs)
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1 AND source = 'bank_import' AND status = 'posted'`, f.club.ID); n != 1 {
		t.Errorf("%d posted bank_import entries, want 1", n)
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1 AND status = 'draft'`, f.club.ID); n != 0 {
		t.Errorf("%d drafts left behind, want 0", n)
	}
	if n := f.scalar(t,
		`SELECT count(*) FROM bank_import_rows b JOIN journal_entries je ON je.id = b.journal_entry_id
		  WHERE b.id = $1 AND je.status = 'posted'`, row); n != 1 {
		t.Errorf("row not linked to the posted entry")
	}
	if n := f.scalar(t, `SELECT count(*) FROM invoices WHERE club_id = $1 AND payment_id IS NOT NULL`, f.club.ID); n != 1 {
		t.Errorf("%d fakturas marked paid, want 1", n)
	}
}

// A manual assignment whose post fails unlinks the row and drops the draft:
// nothing is booked, the faktura stays owed, and the row is assignable again.
func TestAssignBankRowToInvoicePostFailureUnwinds(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	row := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 45000})
	if _, err := f.club.Pool.Exec(ctx, `
		CREATE FUNCTION refuse_assign_post() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'synthetic post failure'; END $$;
		CREATE TRIGGER refuse_assign_post BEFORE UPDATE ON journal_entries FOR EACH ROW
		  WHEN (OLD.source = 'bank_import' AND NEW.status = 'posted')
		  EXECUTE FUNCTION refuse_assign_post();`); err != nil {
		t.Fatalf("fault trigger: %v", err)
	}

	if _, err := f.svc.AssignBankRowToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, row, inv.ID); err == nil {
		t.Fatal("assign succeeded although posting fails")
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID); n != 0 {
		t.Errorf("%d journal entries left after a failed post, want 0", n)
	}
	if n := f.scalar(t, `SELECT count(*) FROM bank_import_rows WHERE id = $1 AND journal_entry_id IS NULL AND invoice_id IS NULL`, row); n != 1 {
		t.Errorf("row still linked after a failed post")
	}
	if n := f.scalar(t, `SELECT count(*) FROM invoices WHERE id = $1 AND payment_id IS NULL`, inv.ID); n != 1 {
		t.Errorf("faktura marked paid after a failed post")
	}
}

// Writers in one period read MAX(entry_number) under a shared lock, so two
// of them can pick the same number. A void and several creates racing in
// one period must all succeed with distinct bilag numbers.
func TestConcurrentWritersInOnePeriodGetDistinctNumbers(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()
	toVoid := f.entry(t, f.club, "1920", "3100", 100, true)

	const creators = 6
	errs := make([]error, creators+1)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < creators; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = f.svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
				FiscalPeriodID: f.club.PeriodID,
				EntryDate:      time.Date(f.club.Year, 4, 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
				Description:    "Synthetic concurrent entry",
				CreatedBy:      f.club.Treasurer.ID,
				ClubID:         f.club.ID,
				Lines: []CreateJournalLineInput{
					{AccountCode: "1920", Debit: 50},
					{AccountCode: "3100", Credit: 50},
				},
			})
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		_, errs[creators] = f.svc.VoidJournalEntry(ctx, f.club.ID, toVoid, f.club.Treasurer.ID)
	}()
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("writer %d: %v", i, err)
		}
	}
	// The voided entry, its reversal and the six new ones.
	if n := f.scalar(t, `SELECT count(DISTINCT entry_number) FROM journal_entries WHERE fiscal_period_id = $1`, f.club.PeriodID); n != creators+2 {
		t.Errorf("%d distinct bilag numbers, want %d", n, creators+2)
	}
}

// Two rows that share a faktura are unassigned at once. Each run locks the
// same set of entries; in id order neither can deadlock the other, and the
// second run finds the bookings already reversed and only clears its rows.
func TestConcurrentUnassignOfSiblingRowsDoesNotDeadlock(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()

	for round := 0; round < 4; round++ {
		inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 90000, Lines: []fin.Line{{Category: "membership", Cents: 90000}}})
		importID := f.club.NewBankImport(t)
		rows := []string{
			f.club.NewBankRow(t, importID, fin.BankRowOpts{Cents: 45000}),
			f.club.NewBankRow(t, importID, fin.BankRowOpts{Cents: 45000}),
		}
		// A faktura paid in two instalments: each row carries its own
		// posted entry and both point at the faktura.
		for _, row := range rows {
			entry := f.entry(t, f.club, "1920", "1500", 450, true)
			if _, err := f.club.Pool.Exec(ctx,
				`UPDATE bank_import_rows SET journal_entry_id = $1, invoice_id = $2 WHERE id = $3`,
				entry, inv.ID, row,
			); err != nil {
				t.Fatalf("link row: %v", err)
			}
		}

		errs := make([]error, len(rows))
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i, row := range rows {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, _, errs[i] = f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, row)
			}()
		}
		close(start)
		wg.Wait()
		// The second run may find the rows already cleared by the first;
		// "not assigned" is fine. A deadlock (40P01) or anything else isn't.
		for i, err := range errs {
			if err != nil && !errors.Is(err, ErrBankRowNotAssigned) {
				t.Fatalf("round %d unassign %d: %v", round, i, err)
			}
		}
		if n := f.scalar(t, `SELECT count(*) FROM bank_import_rows WHERE bank_import_id = $1 AND journal_entry_id IS NOT NULL`, importID); n != 0 {
			t.Errorf("round %d: %d rows still linked after unassigning both", round, n)
		}
	}
	// Every instalment was reversed exactly once.
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1 AND description LIKE 'Reversering%'`, f.club.ID); n != 8 {
		t.Errorf("%d reversals, want one per booking (8)", n)
	}
	if got := f.net(t, f.club, "1500"); got != 0 {
		t.Errorf("1500 net = %v after reversing every instalment, want 0", got)
	}
}

// A void that arrives while the period is being closed waits for the close
// and is then refused: the reversal never lands in the closed period.
func TestVoidWaitsForInFlightClosePeriod(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()
	entry := f.entry(t, f.club, "1920", "3100", 200, true)

	tx, err := f.club.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT 1 FROM fiscal_periods WHERE id = $1 FOR UPDATE`, f.club.PeriodID); err != nil {
		t.Fatalf("lock period: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE fiscal_periods SET status = 'closed'::fiscal_period_status WHERE id = $1`, f.club.PeriodID,
	); err != nil {
		t.Fatalf("close period: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := f.svc.VoidJournalEntry(context.Background(), f.club.ID, entry, f.club.Treasurer.ID)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("void returned while the period was being closed: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit close: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrPeriodClosed) {
			t.Fatalf("void after the close committed: err = %v, want ErrPeriodClosed", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("void never returned")
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1 AND description LIKE 'Reversering%'`, f.club.ID); n != 0 {
		t.Errorf("a reversal was written into the closed period")
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE id = $1 AND status = 'posted'`, entry); n != 1 {
		t.Errorf("entry no longer posted after a refused void")
	}
}
