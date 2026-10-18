package accounting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

type hygieneFixture struct {
	svc  *Service
	club *fin.Club
}

func newHygieneFixture(t *testing.T) *hygieneFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, nil, zerolog.Nop())
	return &hygieneFixture{svc: svc, club: fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))}
}

// otherClub seeds a second club in the same database, for cross-tenant checks.
func (f *hygieneFixture) otherClub(t *testing.T) *fin.Club {
	t.Helper()
	return fin.NewClub(t, f.club.Pool, fin.WithKontoplan(f.svc.SeedKontoplan))
}

func (f *hygieneFixture) entry(t *testing.T, club *fin.Club, debit, credit string, amount float64, post bool) string {
	t.Helper()
	ctx := context.Background()
	e, err := f.svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: club.PeriodID,
		EntryDate:      time.Date(club.Year, 5, 2, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
		Description:    "Synthetic bilag",
		CreatedBy:      club.Treasurer.ID,
		ClubID:         club.ID,
		Lines: []CreateJournalLineInput{
			{AccountCode: debit, Debit: amount},
			{AccountCode: credit, Credit: amount},
		},
	})
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	if post {
		if err := f.svc.PostJournalEntry(ctx, club.ID, e.ID, club.Treasurer.ID); err != nil {
			t.Fatalf("post entry: %v", err)
		}
	}
	return e.ID
}

func (f *hygieneFixture) setPeriod(t *testing.T, club *fin.Club, status string) {
	t.Helper()
	if _, err := club.Pool.Exec(context.Background(),
		`UPDATE fiscal_periods SET status = $2::fiscal_period_status WHERE id = $1`, club.PeriodID, status,
	); err != nil {
		t.Fatalf("set period %s: %v", status, err)
	}
}

func (f *hygieneFixture) net(t *testing.T, club *fin.Club, code string) float64 {
	t.Helper()
	tb, err := f.svc.TrialBalance(context.Background(), club.ID, club.PeriodID, false)
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	for _, l := range tb.Lines {
		if l.AccountCode == code {
			return roundCents(l.Debit - l.Credit)
		}
	}
	return 0
}

func (f *hygieneFixture) scalar(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return n
}

func (f *hygieneFixture) status(t *testing.T, entryID string) string {
	t.Helper()
	var s string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT status FROM journal_entries WHERE id = $1`, entryID).Scan(&s); err != nil {
		t.Fatalf("entry status: %v", err)
	}
	return s
}

func TestVoidJournalEntryRespectsPeriodStatus(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()
	id := f.entry(t, f.club, "1920", "3900", 1250, true)

	for _, st := range []string{"closed", "locked"} {
		f.setPeriod(t, f.club, st)
		if _, err := f.svc.VoidJournalEntry(ctx, f.club.ID, id, f.club.Treasurer.ID); !errors.Is(err, ErrPeriodClosed) {
			t.Fatalf("void into %s period: err = %v, want ErrPeriodClosed", st, err)
		}
		if got := f.status(t, id); got != "posted" {
			t.Errorf("%s period: entry status = %s, want posted (untouched)", st, got)
		}
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID); n != 1 {
		t.Errorf("refused voids wrote %d entries, want only the original", n-1)
	}

	f.setPeriod(t, f.club, "open")
	if _, err := f.svc.VoidJournalEntry(ctx, f.club.ID, id, f.club.Treasurer.ID); err != nil {
		t.Fatalf("void in open period: %v", err)
	}
	if b, r := f.net(t, f.club, "1920"), f.net(t, f.club, "3900"); b != 0 || r != 0 {
		t.Errorf("after void: 1920 net %v, 3900 net %v; want both 0", b, r)
	}
}

func TestAccountingMutationsAreClubScoped(t *testing.T) {
	f := newHygieneFixture(t)
	other := f.otherClub(t)
	ctx := context.Background()
	actor := other.Treasurer.ID

	draft := f.entry(t, f.club, "1920", "3900", 300, false)
	posted := f.entry(t, f.club, "1920", "3900", 450, true)

	if err := f.svc.PostJournalEntry(ctx, other.ID, draft, actor); !errors.Is(err, ErrEntryNotFound) {
		t.Errorf("post another club's draft: err = %v, want ErrEntryNotFound", err)
	}
	if got := f.status(t, draft); got != "draft" {
		t.Errorf("foreign post changed the draft to %s", got)
	}
	if _, err := f.svc.VoidJournalEntry(ctx, other.ID, posted, actor); !errors.Is(err, ErrEntryNotFound) {
		t.Errorf("void another club's entry: err = %v, want ErrEntryNotFound", err)
	}
	if got := f.status(t, posted); got != "posted" {
		t.Errorf("foreign void changed the entry to %s", got)
	}
	if e, err := f.svc.GetJournalEntry(ctx, other.ID, posted); err != nil || e != nil {
		t.Errorf("get another club's entry = %v, %v; want nil, nil", e, err)
	}

	var accountID, name string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO accounts (club_id, code, name, account_type, is_system) VALUES ($1, '3950', 'Synthetic income', 'revenue', false)
		 RETURNING id, name`, f.club.ID).Scan(&accountID, &name); err != nil {
		t.Fatalf("custom account: %v", err)
	}
	if _, err := f.svc.UpdateAccount(ctx, other.ID, accountID, "Renamed", "", MVANotApplicable); !errors.Is(err, ErrAccountNotFound) {
		t.Errorf("update another club's account: err = %v, want ErrAccountNotFound", err)
	}
	if err := f.svc.DeactivateAccount(ctx, other.ID, accountID); !errors.Is(err, ErrAccountNotFound) {
		t.Errorf("deactivate another club's account: err = %v, want ErrAccountNotFound", err)
	}
	if n := f.scalar(t, `SELECT count(*) FROM accounts WHERE id = $1 AND name = $2 AND is_active`, accountID, name); n != 1 {
		t.Errorf("foreign account mutation changed the account")
	}

	f.setPeriod(t, f.club, "closed")
	if err := f.svc.ReopenPeriod(ctx, other.ID, f.club.PeriodID); err == nil {
		t.Errorf("reopen another club's period succeeded")
	}
	if n := f.scalar(t, `SELECT count(*) FROM fiscal_periods WHERE id = $1 AND status = 'closed'`, f.club.PeriodID); n != 1 {
		t.Errorf("foreign reopen changed the period")
	}
	f.setPeriod(t, f.club, "open")

	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	row := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 45000, KID: inv.KID})
	if _, err := f.svc.AssignBankRowToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, row, inv.ID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, _, err := f.svc.UnassignBankRow(ctx, other.ID, actor, row); !errors.Is(err, ErrBankRowNotFound) {
		t.Errorf("unassign another club's row: err = %v, want ErrBankRowNotFound", err)
	}
	if n := f.scalar(t, `SELECT count(*) FROM bank_import_rows WHERE id = $1 AND journal_entry_id IS NOT NULL`, row); n != 1 {
		t.Errorf("foreign unassign cleared the row")
	}
}

func TestUnassignBankRowPostedReversesAndReopensFaktura(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()
	member := f.club.NewMember(t)
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &member, Lines: []fin.Line{
		{Category: "membership", Cents: 45000},
		{Category: "slip_fee", Cents: 260000},
	}})
	row := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 305000, KID: inv.KID})
	entryID, err := f.svc.AssignBankRowToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, row, inv.ID)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	bankBefore := f.net(t, f.club, "1920")

	if _, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, row); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if got := f.status(t, entryID); got != "voided" {
		t.Errorf("entry status = %s, want voided", got)
	}
	if got := f.net(t, f.club, "1920"); got != bankBefore-3050 {
		t.Errorf("1920 net = %v, want %v (deposit reversed)", got, bankBefore-3050)
	}
	if n := f.scalar(t, `SELECT count(*) FROM invoices WHERE id = $1 AND payment_id IS NULL`, inv.ID); n != 1 {
		t.Errorf("faktura still marked paid after unassign")
	}
	if n := f.scalar(t, `SELECT count(*) FROM bank_import_rows WHERE id = $1 AND journal_entry_id IS NULL AND invoice_id IS NULL`, row); n != 1 {
		t.Errorf("row not returned to the queue")
	}
	if _, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, row); !errors.Is(err, ErrBankRowNotAssigned) {
		t.Errorf("second unassign: err = %v, want ErrBankRowNotAssigned", err)
	}
}

func TestUnassignBankRowDraftIsDeletedNotReversed(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()
	row := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 75000})
	draft := f.entry(t, f.club, "1920", "3900", 750, false)
	if _, err := f.club.Pool.Exec(ctx, `UPDATE bank_import_rows SET journal_entry_id = $1 WHERE id = $2`, draft, row); err != nil {
		t.Fatalf("link draft: %v", err)
	}

	if _, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, row); err != nil {
		t.Fatalf("unassign draft: %v", err)
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID); n != 0 {
		t.Errorf("%d entries remain, want the draft deleted and no reversal", n)
	}
	if n := f.scalar(t, `SELECT count(*) FROM bank_import_rows WHERE id = $1 AND journal_entry_id IS NULL`, row); n != 1 {
		t.Errorf("row still linked")
	}
}

func TestUnassignBankRowRefusesClosedPeriod(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	row := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 45000, KID: inv.KID})
	entryID, err := f.svc.AssignBankRowToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, row, inv.ID)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if err := f.svc.ClosePeriod(ctx, f.club.ID, f.club.PeriodID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("close period: %v", err)
	}

	if _, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, row); !errors.Is(err, ErrPeriodClosed) {
		t.Fatalf("unassign in closed period: err = %v, want ErrPeriodClosed", err)
	}
	if got := f.status(t, entryID); got != "posted" {
		t.Errorf("entry status = %s, want posted (untouched)", got)
	}
	if n := f.scalar(t, `SELECT count(*) FROM bank_import_rows WHERE id = $1 AND journal_entry_id IS NOT NULL`, row); n != 1 {
		t.Errorf("row was cleared despite the refusal")
	}
	if n := f.scalar(t, `SELECT count(*) FROM invoices WHERE id = $1 AND payment_id IS NOT NULL`, inv.ID); n != 1 {
		t.Errorf("faktura reopened despite the refusal")
	}
}

// An intra-bank transfer links both rows to one entry; unassigning either
// must release both and reverse the entry once.
func TestUnassignBankRowSharedTransferEntry(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()
	out := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: -500000})
	in := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 500000})
	entryID := f.entry(t, f.club, "1925", "1920", 5000, true)
	if _, err := f.club.Pool.Exec(ctx,
		`UPDATE bank_import_rows SET journal_entry_id = $1 WHERE id = ANY($2::uuid[])`, entryID, []string{out, in},
	); err != nil {
		t.Fatalf("link transfer: %v", err)
	}

	if _, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, out); err != nil {
		t.Fatalf("unassign transfer: %v", err)
	}
	if n := f.scalar(t, `SELECT count(*) FROM bank_import_rows WHERE id = ANY($1::uuid[]) AND journal_entry_id IS NULL`, []string{out, in}); n != 2 {
		t.Errorf("%d of 2 transfer rows released", n)
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1 AND description LIKE 'Reversering%'`, f.club.ID); n != 1 {
		t.Errorf("%d reversals, want exactly 1", n)
	}
	if a, b := f.net(t, f.club, "1920"), f.net(t, f.club, "1925"); a != 0 || b != 0 {
		t.Errorf("transfer not reversed: 1920 %v, 1925 %v", a, b)
	}
}

// A run interrupted after the void (entry voided, row still linked) is
// finished by a retry without a second reversal.
func TestUnassignBankRowRetryAfterInterruptedVoid(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 120000})
	row := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 120000, KID: inv.KID})
	entryID, err := f.svc.AssignBankRowToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, row, inv.ID)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if _, err := f.svc.VoidJournalEntry(ctx, f.club.ID, entryID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("void (simulated interruption): %v", err)
	}

	if _, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, row); err != nil {
		t.Fatalf("retry unassign: %v", err)
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1 AND description LIKE 'Reversering%'`, f.club.ID); n != 1 {
		t.Errorf("%d reversals after retry, want 1", n)
	}
	if n := f.scalar(t, `SELECT count(*) FROM invoices WHERE id = $1 AND payment_id IS NULL`, inv.ID); n != 1 {
		t.Errorf("retry did not reopen the faktura")
	}
}

// A draft being created while the period is closed must not end up in a
// closed period. The writer holds the period row FOR SHARE (as
// CreateJournalEntry does); ClosePeriod's FOR UPDATE waits for it and then
// sees the committed draft.
func TestClosePeriodWaitsForInFlightDraft(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()

	tx, err := f.club.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM fiscal_periods WHERE id = $1 FOR SHARE`, f.club.PeriodID).Scan(&status); err != nil {
		t.Fatalf("share-lock period: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO journal_entries (club_id, fiscal_period_id, entry_number, entry_date, description, created_by)
		 VALUES ($1, $2, 1, $3, 'In-flight draft', $4)`,
		f.club.ID, f.club.PeriodID, time.Date(f.club.Year, 3, 1, 0, 0, 0, 0, time.UTC), f.club.Treasurer.ID,
	); err != nil {
		t.Fatalf("in-flight draft: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- f.svc.ClosePeriod(context.Background(), f.club.ID, f.club.PeriodID, f.club.Treasurer.ID)
	}()

	select {
	case err := <-done:
		t.Fatalf("ClosePeriod returned while a writer held the period: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit draft: %v", err)
	}

	select {
	case err := <-done:
		var drafts *PeriodHasDraftsError
		if !errors.As(err, &drafts) {
			t.Fatalf("ClosePeriod after the draft committed: err = %v, want PeriodHasDraftsError", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ClosePeriod never returned")
	}
	if n := f.scalar(t, `SELECT count(*) FROM fiscal_periods WHERE id = $1 AND status = 'open'`, f.club.PeriodID); n != 1 {
		t.Errorf("period closed around an in-flight draft")
	}
}

// An unassign that reads a row's entry while a manual assign is still
// posting it must wait for the post and then reverse the now-posted entry,
// never unlink the row and leave a posted entry behind.
func TestUnassignBankRowWaitsForInFlightPost(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()
	row := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 75000})
	draft := f.entry(t, f.club, "1920", "3900", 750, false)
	if _, err := f.club.Pool.Exec(ctx, `UPDATE bank_import_rows SET journal_entry_id = $1 WHERE id = $2`, draft, row); err != nil {
		t.Fatalf("link draft: %v", err)
	}

	// Hold the entry row the way PostJournalEntry does, mid-post.
	holder, err := f.club.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	var holderPID int
	if err := holder.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatalf("holder pid: %v", err)
	}
	if _, err := holder.Exec(ctx,
		`UPDATE journal_entries SET status = 'posted', posted_by = $2, posted_at = now() WHERE id = $1`,
		draft, f.club.Treasurer.ID); err != nil {
		t.Fatalf("hold post: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, row)
		done <- err
	}()

	// Wait until the unassign is blocked on the holder's lock.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := f.club.Pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))`, holderPID,
		).Scan(&waiting); err != nil {
			t.Fatalf("poll waiters: %v", err)
		}
		if waiting > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("unassign returned (%v) without waiting for the in-flight post", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("unassign never blocked on the in-flight post")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := holder.Commit(ctx); err != nil {
		t.Fatalf("commit holder: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("unassign after post: %v", err)
	}

	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE id = $1 AND status = 'voided'`, draft); n != 1 {
		t.Errorf("posted entry was not reversed after the unassign waited for it")
	}
	if n := f.scalar(t, `SELECT count(*) FROM bank_import_rows WHERE id = $1 AND journal_entry_id IS NULL`, row); n != 1 {
		t.Errorf("row still linked")
	}
}

// Unassigning an account booking (no receivables line) must not touch a
// faktura that happens to be paid the same day for the same amount: only a
// faktura booking can be traced back to a faktura.
func TestUnassignAccountBookingLeavesSameDayPaidFakturaAlone(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()
	day := time.Date(f.club.Year, 6, 3, 0, 0, 0, 0, time.UTC)
	member := f.club.NewMember(t)
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &member, Cents: 100000, Paid: true})
	if _, err := f.club.Pool.Exec(ctx,
		`UPDATE payments SET paid_at = $1 WHERE id = (SELECT payment_id FROM invoices WHERE id = $2)`, day, inv.ID,
	); err != nil {
		t.Fatalf("date payment: %v", err)
	}
	row := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 100000, Date: day})
	if _, err := f.svc.AssignBankRowToAccount(ctx, f.club.ID, f.club.Treasurer.ID, row, "3900", "revenue", ""); err != nil {
		t.Fatalf("assign account: %v", err)
	}

	_, priorInvoice, err := f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, row)
	if err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if priorInvoice != "" {
		t.Errorf("unassign recovered faktura %s from an account booking", priorInvoice)
	}
	if n := f.scalar(t, `SELECT count(*) FROM invoices WHERE id = $1 AND payment_id IS NOT NULL`, inv.ID); n != 1 {
		t.Errorf("same-day paid faktura lost its payment")
	}
}
