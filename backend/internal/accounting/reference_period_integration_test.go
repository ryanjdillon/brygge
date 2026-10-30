package accounting

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// refFixture is a club whose current year is open and whose previous year is
// a second period. The calendar year is dynamic because marking the year that
// covers today is refused.
type refFixture struct {
	svc       *Service
	club      *fin.Club
	refYear   int
	refPeriod string
}

func newRefFixture(t *testing.T, mark bool) *refFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, nil, zerolog.Nop())
	cur := time.Now().Year()
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan), fin.WithYear(cur))
	f := &refFixture{svc: svc, club: club, refYear: cur - 1}
	f.refPeriod = addPeriod(t, club, f.refYear)
	if mark {
		f.mark(t, true)
	}
	return f
}

func (f *refFixture) mark(t *testing.T, on bool) {
	t.Helper()
	if _, _, err := f.svc.SetPeriodReference(context.Background(), f.club.ID, f.refPeriod, on); err != nil {
		t.Fatalf("set reference %v: %v", on, err)
	}
}

// ensureAccount adds an account the default kontoplan lacks.
func ensureAccount(t *testing.T, club *fin.Club, code, accountType string) {
	t.Helper()
	if _, err := club.Pool.Exec(context.Background(),
		`INSERT INTO accounts (club_id, code, name, account_type, is_system)
		 VALUES ($1, $2, 'Synthetic ' || $2, $3::account_type, false)
		 ON CONFLICT (club_id, code) DO NOTHING`,
		club.ID, code, accountType,
	); err != nil {
		t.Fatalf("account %s: %v", code, err)
	}
}

type refLine struct {
	code   string
	debit  float64
	credit float64
}

func (f *refFixture) create(club *fin.Club, periodID string, year int, source string, lines ...refLine) (*JournalEntry, error) {
	in := CreateJournalEntryInput{
		FiscalPeriodID: periodID,
		EntryDate:      dateIn(year, 6, 2),
		Description:    "Synthetic bilag",
		Source:         source,
		CreatedBy:      club.Treasurer.ID,
		ClubID:         club.ID,
	}
	for _, l := range lines {
		in.Lines = append(in.Lines, CreateJournalLineInput{AccountCode: l.code, Debit: l.debit, Credit: l.credit})
	}
	return f.svc.CreateJournalEntry(context.Background(), in)
}

func (f *refFixture) createPosted(t *testing.T, periodID string, year int, lines ...refLine) string {
	t.Helper()
	e, err := f.create(f.club, periodID, year, "", lines...)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := f.svc.PostJournalEntry(context.Background(), f.club.ID, e.ID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("post: %v", err)
	}
	return e.ID
}

func (f *refFixture) scalar(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return n
}

func twoLines(debit, credit string) []refLine {
	return []refLine{{code: debit, debit: 100}, {code: credit, credit: 100}}
}

func TestReferencePeriodGuardCreate(t *testing.T) {
	f := newRefFixture(t, true)
	ensureAccount(t, f.club, "1960", "asset")
	if _, err := f.club.Pool.Exec(context.Background(),
		`INSERT INTO club_bank_accounts (club_id, account_number, role, gl_code) VALUES ($1, $2, 'other', '1960')`,
		f.club.ID, "8888"+testutil.RandomHex(4)); err != nil {
		t.Fatalf("bank account: %v", err)
	}

	allowed := []struct{ name, debit, credit string }{
		{"outgoing bank row to a cost account", "6200", "1920"},
		{"cost to cost reclassification", "6200", "6100"},
		{"supplier refund into the bank", "1920", "6500"},
		{"cost paid from a bank account only the club's list names", "7700", "1960"},
	}
	for _, tc := range allowed {
		t.Run("allows "+tc.name, func(t *testing.T) {
			if _, err := f.create(f.club, f.refPeriod, f.refYear, "", twoLines(tc.debit, tc.credit)...); err != nil {
				t.Fatalf("%s/%s refused: %v", tc.debit, tc.credit, err)
			}
		})
	}

	refused := []struct {
		name          string
		debit, credit string
		source        string
		extra         []refLine
	}{
		{name: "revenue", debit: "1920", credit: "3100"},
		{name: "receivables", debit: "1920", credit: "1500"},
		{name: "Vipps clearing 2900", debit: "1920", credit: "2900"},
		{name: "cost paid from Vipps clearing", debit: "6200", credit: "2900"},
		{name: "Stripe clearing 1580", debit: "6200", credit: "1580"},
		{name: "bank to bank", debit: "1925", credit: "1920"},
		{name: "supplier payment, no cost line", debit: "2400", credit: "1920"},
		{name: "opening balance", debit: "1920", credit: "2050", source: "opening_balance"},
		{name: "cost and receivables mixed", debit: "6200", credit: "1920", extra: []refLine{{code: "1500", credit: 40}}},
	}
	for _, tc := range refused {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			lines := twoLines(tc.debit, tc.credit)
			if tc.extra != nil {
				lines = []refLine{{code: tc.debit, debit: 100}, {code: tc.credit, credit: 60}, tc.extra[0]}
			}
			_, err := f.create(f.club, f.refPeriod, f.refYear, tc.source, lines...)
			if !errors.Is(err, ErrReferencePeriod) {
				t.Fatalf("%s/%s: err = %v, want ErrReferencePeriod", tc.debit, tc.credit, err)
			}
		})
	}

	t.Run("refuses a configured clearing account inside the cash range", func(t *testing.T) {
		ensureAccount(t, f.club, "1940", "asset")
		if _, err := f.club.Pool.Exec(context.Background(),
			`UPDATE clubs SET vipps_clearing_account = '1940' WHERE id = $1`, f.club.ID); err != nil {
			t.Fatalf("configure clearing: %v", err)
		}
		if _, err := f.create(f.club, f.refPeriod, f.refYear, "", twoLines("6200", "1940")...); !errors.Is(err, ErrReferencePeriod) {
			t.Fatalf("cost paid from clearing 1940: err = %v, want ErrReferencePeriod", err)
		}
	})

	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1 AND fiscal_period_id = $2`, f.club.ID, f.refPeriod); n != len(allowed) {
		t.Errorf("reference period holds %d entries, want only the %d allowed", n, len(allowed))
	}
}

// The entry date counts as well as the target period: a deposit dated in a
// reference year must not become income of another year.
func TestReferencePeriodGuardKeysOnEntryDate(t *testing.T) {
	f := newRefFixture(t, true)
	if _, err := f.create(f.club, f.club.PeriodID, f.refYear, "", twoLines("1920", "3100")...); !errors.Is(err, ErrReferencePeriod) {
		t.Fatalf("revenue dated in the reference year into the open year: err = %v, want ErrReferencePeriod", err)
	}
	if _, err := f.create(f.club, f.club.PeriodID, f.refYear, "", twoLines("6200", "1920")...); err != nil {
		t.Fatalf("cost dated in the reference year into the open year: %v", err)
	}
	if _, err := f.create(f.club, f.club.PeriodID, f.club.Year, "", twoLines("1920", "3100")...); err != nil {
		t.Fatalf("revenue dated in the open year: %v", err)
	}
}

func TestReferencePeriodGuardIsClubScoped(t *testing.T) {
	f := newRefFixture(t, true)
	other := fin.NewClub(t, f.club.Pool, fin.WithKontoplan(f.svc.SeedKontoplan), fin.WithYear(f.club.Year))
	otherPrev := addPeriod(t, other, f.refYear)
	if _, err := f.create(other, otherPrev, f.refYear, "", twoLines("1920", "3100")...); err != nil {
		t.Fatalf("another club's unmarked previous year refused: %v", err)
	}
	if _, err := f.create(other, other.PeriodID, f.refYear, "", twoLines("1920", "3100")...); err != nil {
		t.Fatalf("another club's entry dated in a year only this club marked: %v", err)
	}
}

func TestReferencePeriodGuardPostAndVoid(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()

	revenueDraft, err := f.create(f.club, f.refPeriod, f.refYear, "", twoLines("1920", "3100")...)
	if err != nil {
		t.Fatalf("revenue draft: %v", err)
	}
	costDraft, err := f.create(f.club, f.refPeriod, f.refYear, "", twoLines("6200", "1920")...)
	if err != nil {
		t.Fatalf("cost draft: %v", err)
	}
	receivable := f.createPosted(t, f.refPeriod, f.refYear, twoLines("1920", "1500")...)
	cost := f.createPosted(t, f.refPeriod, f.refYear, twoLines("6500", "1920")...)
	f.mark(t, true)

	actor := f.club.Treasurer.ID
	if err := f.svc.PostJournalEntry(ctx, f.club.ID, revenueDraft.ID, actor); !errors.Is(err, ErrReferencePeriod) {
		t.Errorf("post a non-expense draft: err = %v, want ErrReferencePeriod", err)
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE id = $1 AND status = 'draft'`, revenueDraft.ID); n != 1 {
		t.Errorf("refused post changed the draft")
	}
	if err := f.svc.PostJournalEntry(ctx, f.club.ID, costDraft.ID, actor); err != nil {
		t.Errorf("post an expense-shaped draft: %v", err)
	}

	if _, err := f.svc.VoidJournalEntry(ctx, f.club.ID, receivable, actor); !errors.Is(err, ErrReferencePeriod) {
		t.Errorf("void a posted 1500 entry: err = %v, want ErrReferencePeriod", err)
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE id = $1 AND status = 'posted'`, receivable); n != 1 {
		t.Errorf("refused void changed the entry")
	}
	reversal, err := f.svc.VoidJournalEntry(ctx, f.club.ID, cost, actor)
	if err != nil {
		t.Fatalf("void a posted cost entry: %v", err)
	}
	if reversal.FiscalPeriodID != f.refPeriod {
		t.Errorf("reversal landed in period %s, want the reference period", reversal.FiscalPeriodID)
	}
}

func TestReferencePeriodNonReferenceUnaffected(t *testing.T) {
	f := newRefFixture(t, false)
	if _, err := f.create(f.club, f.refPeriod, f.refYear, "", twoLines("1920", "3100")...); err != nil {
		t.Fatalf("revenue in an unmarked previous year: %v", err)
	}
}

func TestSetPeriodReference(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()
	other := fin.NewClub(t, f.club.Pool, fin.WithKontoplan(f.svc.SeedKontoplan), fin.WithYear(f.club.Year))

	t.Run("refuses the year covering today", func(t *testing.T) {
		if _, _, err := f.svc.SetPeriodReference(ctx, f.club.ID, f.club.PeriodID, true); !errors.Is(err, ErrReferenceCurrentYear) {
			t.Fatalf("err = %v, want ErrReferenceCurrentYear", err)
		}
	})

	t.Run("refuses a year holding an opening balance, draft included", func(t *testing.T) {
		year := f.refYear - 1
		p := addPeriod(t, f.club, year)
		if _, err := f.create(f.club, p, year, "opening_balance", twoLines("1920", "2050")...); err != nil {
			t.Fatalf("opening balance draft: %v", err)
		}
		if _, _, err := f.svc.SetPeriodReference(ctx, f.club.ID, p, true); !errors.Is(err, ErrReferenceHasOpeningBalance) {
			t.Fatalf("err = %v, want ErrReferenceHasOpeningBalance", err)
		}
		periods, err := f.svc.ListPeriods(ctx, f.club.ID)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		reasons := map[int]string{}
		for _, per := range periods {
			if per.ReferenceBlocked != nil {
				reasons[per.Year] = *per.ReferenceBlocked
			}
		}
		if reasons[year] != "opening_balance" || reasons[f.club.Year] != "current_year" {
			t.Errorf("reference_blocked = %v, want opening_balance for %d and current_year for %d", reasons, year, f.club.Year)
		}
		if _, ok := reasons[f.refYear]; ok {
			t.Errorf("%d is markable but reference_blocked = %q", f.refYear, reasons[f.refYear])
		}
	})

	t.Run("another club's id is not found", func(t *testing.T) {
		if _, _, err := f.svc.SetPeriodReference(ctx, other.ID, f.refPeriod, true); !errors.Is(err, ErrPeriodNotFound) {
			t.Fatalf("err = %v, want ErrPeriodNotFound", err)
		}
		if n := f.scalar(t, `SELECT count(*) FROM fiscal_periods WHERE id = $1 AND NOT reference_only`, f.refPeriod); n != 1 {
			t.Errorf("a foreign id changed the period")
		}
	})

	t.Run("marking, the same value again, and unmarking", func(t *testing.T) {
		p, changed, err := f.svc.SetPeriodReference(ctx, f.club.ID, f.refPeriod, true)
		if err != nil || !changed || !p.ReferenceOnly {
			t.Fatalf("mark: %+v changed=%v err=%v", p, changed, err)
		}
		if _, changed, err = f.svc.SetPeriodReference(ctx, f.club.ID, f.refPeriod, true); err != nil || changed {
			t.Fatalf("mark again: changed=%v err=%v, want a no-op", changed, err)
		}
		p, changed, err = f.svc.SetPeriodReference(ctx, f.club.ID, f.refPeriod, false)
		if err != nil || !changed || p.ReferenceOnly {
			t.Fatalf("unmark: %+v changed=%v err=%v", p, changed, err)
		}
	})

	t.Run("unmarking needs the period open", func(t *testing.T) {
		f.mark(t, true)
		if err := f.svc.ClosePeriod(ctx, f.club.ID, f.refPeriod, f.club.Treasurer.ID); err != nil {
			t.Fatalf("close: %v", err)
		}
		if _, _, err := f.svc.SetPeriodReference(ctx, f.club.ID, f.refPeriod, false); !errors.Is(err, ErrPeriodClosed) {
			t.Fatalf("unmark closed: err = %v, want ErrPeriodClosed", err)
		}
		if err := f.svc.ReopenPeriod(ctx, f.club.ID, f.refPeriod); err != nil {
			t.Fatalf("reopen: %v", err)
		}
		if _, _, err := f.svc.SetPeriodReference(ctx, f.club.ID, f.refPeriod, false); err != nil {
			t.Fatalf("unmark reopened: %v", err)
		}
	})
}

// Marking takes the period FOR UPDATE, so it waits for a booking in flight
// (which holds the period FOR SHARE) instead of flipping the flag under it.
func TestSetPeriodReferenceWaitsForInFlightBooking(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()

	tx, err := f.club.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM fiscal_periods WHERE id = $1 FOR SHARE`, f.refPeriod).Scan(&status); err != nil {
		t.Fatalf("share-lock period: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, _, err := f.svc.SetPeriodReference(context.Background(), f.club.ID, f.refPeriod, true)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("SetPeriodReference returned while a writer held the period: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SetPeriodReference after the writer committed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SetPeriodReference never returned")
	}
}

func TestClosePeriodReferenceSkipsDraftCheck(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()
	actor := f.club.Treasurer.ID

	if _, err := f.create(f.club, f.refPeriod, f.refYear, "", twoLines("1920", "3100")...); err != nil {
		t.Fatalf("draft: %v", err)
	}
	var drafts *PeriodHasDraftsError
	if err := f.svc.ClosePeriod(ctx, f.club.ID, f.refPeriod, actor); !errors.As(err, &drafts) {
		t.Fatalf("close non-reference with a draft: err = %v, want PeriodHasDraftsError", err)
	}

	f.mark(t, true)
	if err := f.svc.ClosePeriod(ctx, f.club.ID, f.refPeriod, actor); err != nil {
		t.Fatalf("close a reference period with drafts: %v", err)
	}
	if _, err := f.create(f.club, f.refPeriod, f.refYear, "", twoLines("6200", "1920")...); !errors.Is(err, ErrPeriodClosed) {
		t.Fatalf("expense into a closed reference period: err = %v, want ErrPeriodClosed", err)
	}
}

// Every booking is inserted by journal.go, which is where the reference-period
// guard sits. A write elsewhere would bypass it, so this lists every other
// INSERT or UPDATE of journal_entries and journal_lines in non-test code.
func TestJournalWritesOnlyThroughTheGuard(t *testing.T) {
	// file (relative to backend/) -> substring of the statement it may run.
	allowed := map[string][]string{
		// The attachment link changes no booking field; the reassign swaps the
		// bank-side account of posted lines and refuses reference years itself.
		"internal/handlers/accounting.go": {"SET attachment_url", "SET account_id = $1 WHERE account_id = $2"},
	}
	write := regexp.MustCompile(`(?is)\b(INSERT\s+INTO|UPDATE)\s+(journal_entries|journal_lines)\b[^` + "`" + `]{0,200}`)

	root := filepath.Join("..", "..")
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" || d.Name() == "gen" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if rel == "internal/accounting/journal.go" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range write.FindAllString(string(b), -1) {
			m = strings.Join(strings.Fields(m), " ")
			ok := false
			for _, sub := range allowed[rel] {
				if strings.Contains(m, sub) {
					ok = true
				}
			}
			if !ok {
				offenders = append(offenders, fmt.Sprintf("%s: %s", rel, m))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("journal writes outside journal.go bypass the reference-period guard:\n%s", strings.Join(offenders, "\n"))
	}
}

// A faktura booked in a year later marked reference can't be reversed there
// (a void needs its 1500 line), so ReverseInvoice refuses as it does for a
// closed year, and the period list counts the booking for the mark dialog.
func TestReverseInvoiceRefusesReferencePeriod(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()
	actor := f.club.Treasurer.ID

	inv := f.club.NewInvoice(t, fin.InvoiceOpts{
		Cents: 45000, IssueDate: time.Date(f.refYear, 3, 1, 0, 0, 0, 0, time.UTC),
	})
	if _, err := f.svc.BookInvoice(ctx, f.club.ID, inv.ID, actor); err != nil {
		t.Fatalf("book faktura: %v", err)
	}
	openBookings := func() int {
		periods, err := f.svc.ListPeriods(ctx, f.club.ID)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, p := range periods {
			if p.ID == f.refPeriod {
				return p.OpenInvoiceBookings
			}
		}
		t.Fatalf("reference period missing from the list")
		return 0
	}
	if n := openBookings(); n != 1 {
		t.Errorf("open_invoice_bookings = %d, want 1", n)
	}

	f.mark(t, true)
	if _, err := f.svc.ReverseInvoice(ctx, f.club.ID, inv.ID, actor); !errors.Is(err, ErrInvoiceReferencePeriod) || !errors.Is(err, ErrReferencePeriod) {
		t.Fatalf("reverse in a reference year: err = %v, want ErrInvoiceReferencePeriod", err)
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1 AND status = 'voided'`, f.club.ID); n != 0 {
		t.Errorf("refused reversal voided %d entries", n)
	}
}
