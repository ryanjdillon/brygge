package accounting

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// transferRowState is what a refused pair request must leave unchanged.
type transferRowState struct {
	entry                                 string
	autoMatched, declined, dismissed, ref bool
}

func readTransferRow(t *testing.T, c *fin.Club, rowID string) transferRowState {
	t.Helper()
	var s transferRowState
	if err := c.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(journal_entry_id::text, ''), auto_matched, auto_declined_at IS NOT NULL,
		        dismissed_at IS NOT NULL, refund_paired_with IS NOT NULL
		   FROM bank_import_rows WHERE id = $1`, rowID,
	).Scan(&s.entry, &s.autoMatched, &s.declined, &s.dismissed, &s.ref); err != nil {
		t.Fatalf("read bank row: %v", err)
	}
	return s
}

func clubEntries(t *testing.T, c *fin.Club) int {
	t.Helper()
	var n int
	if err := c.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM journal_entries WHERE club_id = $1`, c.ID).Scan(&n); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	return n
}

func clubPeriodYears(t *testing.T, c *fin.Club) []int {
	t.Helper()
	rows, err := c.Pool.Query(context.Background(),
		`SELECT year FROM fiscal_periods WHERE club_id = $1 ORDER BY year`, c.ID)
	if err != nil {
		t.Fatalf("periods: %v", err)
	}
	defer rows.Close()
	var years []int
	for rows.Next() {
		var y int
		if err := rows.Scan(&y); err != nil {
			t.Fatalf("scan period: %v", err)
		}
		years = append(years, y)
	}
	return years
}

// transferEntry is a booked transfer's entry as the tests check it.
type transferEntry struct {
	status, createdBy, date, period string
	lines                           map[string][2]float64 // code → (debit, credit)
}

func readTransferEntry(t *testing.T, c *fin.Club, entryID string) transferEntry {
	t.Helper()
	ctx := context.Background()
	e := transferEntry{lines: map[string][2]float64{}}
	if err := c.Pool.QueryRow(ctx,
		`SELECT status::text, created_by::text, entry_date::text, fiscal_period_id::text
		   FROM journal_entries WHERE id = $1`, entryID,
	).Scan(&e.status, &e.createdBy, &e.date, &e.period); err != nil {
		t.Fatalf("read entry: %v", err)
	}
	rows, err := c.Pool.Query(ctx,
		`SELECT a.code, jl.debit::float8, jl.credit::float8
		   FROM journal_lines jl JOIN accounts a ON a.id = jl.account_id
		  WHERE jl.journal_entry_id = $1`, entryID)
	if err != nil {
		t.Fatalf("read lines: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		var d, cr float64
		if err := rows.Scan(&code, &d, &cr); err != nil {
			t.Fatalf("scan line: %v", err)
		}
		e.lines[code] = [2]float64{d, cr}
	}
	return e
}

// transferPairRows seeds an outgoing row on 1920 and an incoming row on 1925
// for the same amount.
func transferPairRows(t *testing.T, c *fin.Club, cents int64, outDate, inDate time.Time) (out, in, outImport string) {
	t.Helper()
	outImport = c.NewBankImportOn(t, "1920")
	out = c.NewBankRow(t, outImport, fin.BankRowOpts{Date: outDate, Cents: -cents, Description: "Synthetic transfer out"})
	in = c.NewBankRow(t, c.NewBankImportOn(t, "1925"), fin.BankRowOpts{Date: inDate, Cents: cents, Description: "Synthetic transfer in"})
	return out, in, outImport
}

// The way back after a person undid an automatic transfer pair: Par som
// overføring books both rows as one posted bilag, as the person, lifts both
// declines, and the next sync leaves the pair alone.
func TestPairBankTransferRepairsUndoneAutomaticPair(t *testing.T) {
	svc, c := newStickyClub(t)
	ctx := context.Background()
	rowA, rowB, _ := transferPairRows(t, c, 100000, day(c.Year, 6, 1), day(c.Year, 6, 1))
	sync := func() *BankSyncResult {
		t.Helper()
		s, err := svc.BankSync(ctx, c.ID, c.Treasurer.ID)
		if err != nil {
			t.Fatalf("BankSync: %v", err)
		}
		return s
	}
	if s := sync(); s.TransfersLinked != 1 {
		t.Fatalf("first sync linked %d transfers, want 1", s.TransfersLinked)
	}
	if _, err := svc.UnassignBankRowReported(ctx, c.ID, c.Treasurer.ID, rowA); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if !declinedBank(t, c, rowA) || !declinedBank(t, c, rowB) {
		t.Fatal("the undo did not decline both rows")
	}

	res, err := svc.PairBankTransfer(ctx, c.ID, c.Treasurer.ID, []string{rowB, rowA}, "")
	if err != nil {
		t.Fatalf("PairBankTransfer: %v", err)
	}
	if res.OutboundRowID != rowA || res.InboundRowID != rowB || res.DebitAccount != "1925" || res.CreditAccount != "1920" ||
		res.Amount != 1000 || res.EntryDate != day(c.Year, 6, 1).Format("2006-01-02") {
		t.Errorf("result = %+v", res)
	}
	lifted := slices.Clone(res.LiftedDecline)
	slices.Sort(lifted)
	want := []string{rowA, rowB}
	slices.Sort(want)
	if !slices.Equal(lifted, want) {
		t.Errorf("LiftedDecline = %v, want both rows", res.LiftedDecline)
	}
	e := readTransferEntry(t, c, res.JournalEntryID)
	if e.status != "posted" || e.createdBy != c.Treasurer.ID {
		t.Errorf("entry status %q created_by %q; want posted by the treasurer", e.status, e.createdBy)
	}
	if e.lines["1925"] != [2]float64{1000, 0} || e.lines["1920"] != [2]float64{0, 1000} || len(e.lines) != 2 {
		t.Errorf("lines = %v; want DR 1925 / CR 1920 1000", e.lines)
	}
	for _, id := range []string{rowA, rowB} {
		if s := readTransferRow(t, c, id); s.entry != res.JournalEntryID || s.autoMatched || s.declined {
			t.Errorf("row %s = %+v; want linked to the entry, not auto-matched, not declined", id, s)
		}
	}

	before := clubEntries(t, c)
	if s := sync(); s.TransfersLinked != 0 {
		t.Errorf("sync after the pair linked %d transfers; want 0", s.TransfersLinked)
	}
	if after := clubEntries(t, c); after != before {
		t.Errorf("sync after the pair changed the entries: %d → %d", before, after)
	}
	if s := readTransferRow(t, c, rowA); s.entry != res.JournalEntryID {
		t.Errorf("sync moved row A to %q", s.entry)
	}
}

// Each refusal keeps nothing: no entry, no year, and both rows as they were.
func TestPairBankTransferRefusals(t *testing.T) {
	svc, c := newStickyClub(t)
	ctx := context.Background()
	y := c.Year
	jun := day(y, 6, 1)

	// Closed and locked years of their own.
	for _, p := range []struct {
		year   int
		status string
	}{{y - 1, "closed"}, {y - 2, "locked"}} {
		stickyExec(t, c, `INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		                  VALUES ($1, $2, make_date($2, 1, 1), make_date($2, 12, 31), $3::fiscal_period_status)`,
			c.ID, p.year, p.status)
	}
	other := fin.NewClub(t, c.Pool, fin.WithKontoplan(svc.SeedKontoplan))

	cases := []struct {
		name    string
		setup   func(t *testing.T) []string
		code    string
		wantErr error
	}{
		{"booked", func(t *testing.T) []string {
			out, in, _ := transferPairRows(t, c, 10000, jun, jun)
			if _, err := svc.AssignBankRowToAccount(ctx, c.ID, c.Treasurer.ID, out, "6200", "expense", ""); err != nil {
				t.Fatalf("book: %v", err)
			}
			return []string{out, in}
		}, TransferRowBooked, nil},
		{"dismissed", func(t *testing.T) []string {
			out, in, _ := transferPairRows(t, c, 10100, jun, jun)
			if err := svc.DismissBankRow(ctx, c.ID, c.Treasurer.ID, in, "internal_transfer"); err != nil {
				t.Fatalf("dismiss: %v", err)
			}
			return []string{out, in}
		}, TransferRowDismissed, nil},
		{"refund-paired", func(t *testing.T) []string {
			out, in, imp := transferPairRows(t, c, 10200, jun, jun)
			third := c.NewBankRow(t, imp, fin.BankRowOpts{Date: jun, Cents: 10200})
			stickyExec(t, c, `UPDATE bank_import_rows SET refund_paired_with = $2 WHERE id = $1`, out, third)
			return []string{out, in}
		}, TransferRowRefundPaired, nil},
		{"provisional", func(t *testing.T) []string {
			out := c.NewBankRow(t, c.NewBankImportOn(t, "1920"), fin.BankRowOpts{Date: jun, Cents: -10300, Description: "Overførsel"})
			in := c.NewBankRow(t, c.NewBankImportOn(t, "1925"), fin.BankRowOpts{Date: jun, Cents: 10300, Description: "Synthetic transfer in"})
			return []string{out, in}
		}, TransferRowProvisional, nil},
		{"same account", func(t *testing.T) []string {
			imp := c.NewBankImportOn(t, "1920")
			return []string{
				c.NewBankRow(t, imp, fin.BankRowOpts{Date: jun, Cents: -10400}),
				c.NewBankRow(t, c.NewBankImportOn(t, "1920"), fin.BankRowOpts{Date: jun, Cents: 10400}),
			}
		}, TransferSameAccount, nil},
		{"same sign", func(t *testing.T) []string {
			return []string{
				c.NewBankRow(t, c.NewBankImportOn(t, "1920"), fin.BankRowOpts{Date: jun, Cents: -10500}),
				c.NewBankRow(t, c.NewBankImportOn(t, "1925"), fin.BankRowOpts{Date: jun, Cents: -10500}),
			}
		}, TransferSameSign, nil},
		{"incoming carries a faktura's KID", func(t *testing.T) []string {
			inv := c.NewInvoice(t, fin.InvoiceOpts{Cents: 10600})
			return []string{
				c.NewBankRow(t, c.NewBankImportOn(t, "1920"), fin.BankRowOpts{Date: jun, Cents: -10600}),
				c.NewBankRow(t, c.NewBankImportOn(t, "1925"), fin.BankRowOpts{Date: jun, Cents: 10600, KID: inv.KID}),
			}
		}, TransferRowHasKID, nil},
		{"amount off by one øre", func(t *testing.T) []string {
			return []string{
				c.NewBankRow(t, c.NewBankImportOn(t, "1920"), fin.BankRowOpts{Date: jun, Cents: -10700}),
				c.NewBankRow(t, c.NewBankImportOn(t, "1925"), fin.BankRowOpts{Date: jun, Cents: 10699}),
			}
		}, TransferAmountMismatch, nil},
		{"15 days apart", func(t *testing.T) []string {
			out, in, _ := transferPairRows(t, c, 10800, day(y, 6, 1), day(y, 6, 16))
			return []string{out, in}
		}, TransferDatesApart, nil},
		{"Dec 31 and Jan 2", func(t *testing.T) []string {
			out, in, _ := transferPairRows(t, c, 10900, day(y, 12, 31), day(y+1, 1, 2))
			return []string{out, in}
		}, TransferCrossesPeriod, nil},
		{"closed year", func(t *testing.T) []string {
			out, in, _ := transferPairRows(t, c, 11000, day(y-1, 6, 1), day(y-1, 6, 1))
			return []string{out, in}
		}, TransferPeriodClosed, nil},
		{"locked year", func(t *testing.T) []string {
			out, in, _ := transferPairRows(t, c, 11100, day(y-2, 6, 1), day(y-2, 6, 1))
			return []string{out, in}
		}, TransferPeriodLocked, nil},
		{"provider payout", func(t *testing.T) []string {
			return []string{
				c.NewBankRow(t, c.NewBankImportOn(t, "1920"), fin.BankRowOpts{Date: jun, Cents: -11200}),
				c.NewBankRow(t, c.NewBankImportOn(t, "1925"), fin.BankRowOpts{Date: jun, Cents: 11200, Description: "Utb. 2000591 Vippsnr 100001"}),
			}
		}, "", ErrProviderPayoutDeposit},
		{"another club's row", func(t *testing.T) []string {
			out := c.NewBankRow(t, c.NewBankImportOn(t, "1920"), fin.BankRowOpts{Date: jun, Cents: -11300})
			in := other.NewBankRow(t, other.NewBankImportOn(t, "1925"), fin.BankRowOpts{Date: jun, Cents: 11300})
			return []string{out, in}
		}, "", ErrBankRowNotFound},
		{"one id twice", func(t *testing.T) []string {
			out := c.NewBankRow(t, c.NewBankImportOn(t, "1920"), fin.BankRowOpts{Date: jun, Cents: -11400})
			return []string{out, out}
		}, "", ErrTransferRowCount},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ids := tc.setup(t)
			before := map[string]transferRowState{}
			for _, id := range ids {
				before[id] = readTransferRow(t, c, id)
			}
			entries, years := clubEntries(t, c), clubPeriodYears(t, c)

			_, err := svc.PairBankTransfer(ctx, c.ID, c.Treasurer.ID, ids, "")
			if tc.code != "" {
				var refusal *TransferRefusalError
				if !errors.As(err, &refusal) || refusal.Code != tc.code || refusal.Msg == "" {
					t.Fatalf("err = %v, want refusal %s", err, tc.code)
				}
			} else if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got := clubEntries(t, c); got != entries {
				t.Errorf("entries %d → %d; a refusal must keep none", entries, got)
			}
			if got := clubPeriodYears(t, c); !slices.Equal(got, years) {
				t.Errorf("years %v → %v; a refusal must open none", years, got)
			}
			for _, id := range ids {
				if got := readTransferRow(t, c, id); got != before[id] {
					t.Errorf("row %s changed: %+v → %+v", id, before[id], got)
				}
			}
		})
	}
}

// A number that passes the KID check digit but is no faktura's KID does not
// make the incoming row a faktura payment; ids are compared canonically.
func TestPairBankTransferIgnoresAKIDNoFakturaHas(t *testing.T) {
	svc, c := newStickyClub(t)
	ctx := context.Background()
	jun := day(c.Year, 6, 9)
	out := c.NewBankRow(t, c.NewBankImportOn(t, "1920"), fin.BankRowOpts{Date: jun, Cents: -20500})
	in := c.NewBankRow(t, c.NewBankImportOn(t, "1925"), fin.BankRowOpts{Date: jun, Cents: 20500, KID: finance.GenerateKID("000", 4242, 1)})
	if _, err := svc.PairBankTransfer(ctx, c.ID, c.Treasurer.ID, []string{out, in, strings.ToUpper(in)}, ""); err != nil {
		t.Fatalf("PairBankTransfer: %v", err)
	}
}

// A person's transfer may open the year, like the manual Tildel assigns;
// transfer detection never does (TestTransferDetectionNeverCreatesFiscalPeriod).
func TestPairBankTransferOpensMissingYear(t *testing.T) {
	svc, c := newStickyClub(t)
	ctx := context.Background()
	y := c.Year + 1
	out, in, _ := transferPairRows(t, c, 25000, day(y, 3, 1), day(y, 3, 3))

	res, err := svc.PairBankTransfer(ctx, c.ID, c.Treasurer.ID, []string{out, in}, "Synthetic move to savings")
	if err != nil {
		t.Fatalf("PairBankTransfer: %v", err)
	}
	var periodID string
	if err := c.Pool.QueryRow(ctx, `SELECT id::text FROM fiscal_periods WHERE club_id = $1 AND year = $2`, c.ID, y).Scan(&periodID); err != nil {
		t.Fatalf("the %d period was not created: %v", y, err)
	}
	e := readTransferEntry(t, c, res.JournalEntryID)
	if e.period != periodID || e.status != "posted" || e.date != day(y, 3, 3).Format("2006-01-02") {
		t.Errorf("entry = %+v; want posted in %d on the later date", e, y)
	}
	var desc string
	if err := c.Pool.QueryRow(ctx, `SELECT description FROM journal_entries WHERE id = $1`, res.JournalEntryID).Scan(&desc); err != nil || desc != "Intern overføring: Synthetic move to savings" {
		t.Errorf("description = %q (%v)", desc, err)
	}
}

// Dates may differ by up to 14 days in one fiscal year; the entry is dated
// on the later one.
func TestPairBankTransferDifferentDatesUsesLaterDate(t *testing.T) {
	svc, c := newStickyClub(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		out, in  time.Time
		wantDate time.Time
	}{
		{"in two days later", day(c.Year, 5, 10), day(c.Year, 5, 12), day(c.Year, 5, 12)},
		{"out 14 days later", day(c.Year, 5, 28), day(c.Year, 5, 14), day(c.Year, 5, 28)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, in, _ := transferPairRows(t, c, 30000, tc.out, tc.in)
			res, err := svc.PairBankTransfer(ctx, c.ID, c.Treasurer.ID, []string{out, in}, "")
			if err != nil {
				t.Fatalf("PairBankTransfer: %v", err)
			}
			if want := tc.wantDate.Format("2006-01-02"); res.EntryDate != want || readTransferEntry(t, c, res.JournalEntryID).date != want {
				t.Errorf("entry date %s, want %s", res.EntryDate, want)
			}
		})
	}
}

// A bank-to-bank entry is never allowed in a reference year.
func TestPairBankTransferRefusesReferenceYear(t *testing.T) {
	svc, c := newStickyClub(t)
	ctx := context.Background()
	y := c.Year - 1
	var periodID string
	if err := c.Pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, $2, make_date($2, 1, 1), make_date($2, 12, 31), 'open') RETURNING id`, c.ID, y,
	).Scan(&periodID); err != nil {
		t.Fatalf("period: %v", err)
	}
	if _, _, err := svc.SetPeriodReference(ctx, c.ID, periodID, true); err != nil {
		t.Fatalf("mark reference: %v", err)
	}
	out, in, _ := transferPairRows(t, c, 40000, day(y, 9, 1), day(y, 9, 1))
	entries := clubEntries(t, c)
	if _, err := svc.PairBankTransfer(ctx, c.ID, c.Treasurer.ID, []string{out, in}, ""); !errors.Is(err, ErrReferencePeriod) {
		t.Fatalf("err = %v, want ErrReferencePeriod", err)
	}
	if clubEntries(t, c) != entries || readTransferRow(t, c, out).entry != "" || readTransferRow(t, c, in).entry != "" {
		t.Error("a refused reference-year pair kept something")
	}
}

// pairRace runs a booking prepared by prep and PairBankTransfer on the same
// pair at once, several times, and checks exactly one booking of the rows
// each time. prep runs on the test goroutine (it may seed fixtures).
func pairRace(t *testing.T, c *fin.Club, svc *Service, prep func(out, in, outImport string) func() error, otherLost func(error) bool) {
	t.Helper()
	ctx := context.Background()
	for i := range 6 {
		out, in, outImport := transferPairRows(t, c, int64(50000+i), day(c.Year, 7, 1+i), day(c.Year, 7, 1+i))
		book := prep(out, in, outImport)
		entries := clubEntries(t, c)
		var (
			wg               sync.WaitGroup
			pairErr, bookErr error
		)
		wg.Add(2)
		go func() { defer wg.Done(); bookErr = book() }()
		go func() {
			defer wg.Done()
			// Odd rounds give the other booking a head start, so both
			// orders are exercised.
			if i%2 == 1 {
				time.Sleep(time.Duration(10*i) * time.Millisecond)
			}
			_, pairErr = svc.PairBankTransfer(ctx, c.ID, c.Treasurer.ID, []string{out, in}, "")
		}()
		wg.Wait()

		var refusal *TransferRefusalError
		pairLost := errors.As(pairErr, &refusal) && refusal.Code == TransferRowBooked
		if pairErr != nil && !pairLost {
			t.Fatalf("round %d: pair err = %v, want nil or row_booked", i, pairErr)
		}
		if bookErr != nil && !otherLost(bookErr) {
			t.Fatalf("round %d: other booking err = %v", i, bookErr)
		}
		if got := clubEntries(t, c) - entries; got != 1 {
			t.Fatalf("round %d: %d new entries (pair err %v, other err %v); want exactly one, no orphan draft", i, got, pairErr, bookErr)
		}
		if readTransferRow(t, c, in).entry == "" {
			t.Fatalf("round %d: the incoming row is unbooked", i)
		}
		t.Logf("round %d: pair won = %v", i, pairErr == nil)
	}
}

// Transfer detection and a person's pair on the same undeclined pair, on a
// pool of two connections: one entry, and the loser keeps nothing.
func TestPairBankTransferAndDetectorBookOnceOnSmallPool(t *testing.T) {
	svc, c := newStickyClub(t, testutil.WithMaxConns(2))
	ctx := context.Background()
	pairRace(t, c, svc, func(_, _, outImport string) func() error {
		return func() error {
			_, _, _, err := svc.detectIntraBankTransfers(ctx, c.ID, outImport, "", c.Treasurer.ID)
			return err
		}
	}, func(error) bool { return false })
}

// An account assign of the incoming row against a person's pair.
func TestPairBankTransferAndAccountAssignBookOnce(t *testing.T) {
	svc, c := newStickyClub(t, testutil.WithMaxConns(2))
	ctx := context.Background()
	pairRace(t, c, svc, func(_, in, _ string) func() error {
		return func() error {
			_, err := svc.AssignBankRowToAccount(ctx, c.ID, c.Treasurer.ID, in, "3900", "revenue", "")
			return err
		}
	}, func(err error) bool { return errors.Is(err, errBankRowRaced) })
}

// The KID cascade booking the incoming row against a person's pair.
func TestPairBankTransferAndKIDCascadeBookOnce(t *testing.T) {
	svc, c := newStickyClub(t, testutil.WithMaxConns(2))
	ctx := context.Background()
	pairRace(t, c, svc, func(_, in, _ string) func() error {
		var cents int64
		var date time.Time
		if err := c.Pool.QueryRow(ctx, `SELECT round(amount * 100)::bigint, row_date FROM bank_import_rows WHERE id = $1`, in).Scan(&cents, &date); err != nil {
			t.Fatalf("read row: %v", err)
		}
		inv := c.NewInvoice(t, fin.InvoiceOpts{Cents: cents})
		m := kidCascadeMatch{
			rowID: in, invoiceID: inv.ID, actorID: c.Treasurer.ID, bankAccount: "1925",
			kid: inv.KID, desc: "Synthetic", date: date, amount: fin.Kr(cents),
		}
		return func() error { return svc.bookKIDCascadeMatch(ctx, c.ID, m) }
	}, func(err error) bool { return errors.Is(err, errKIDRowTaken) })
}

// The incoming row assigned to a faktura by hand against a person's pair:
// the assign takes the faktura, then the row, then a bilag number, the pair
// the rows, then a number, so neither deadlocks on the bilag number.
func TestPairBankTransferAndInvoiceAssignBookOnce(t *testing.T) {
	svc, c := newStickyClub(t, testutil.WithMaxConns(2))
	ctx := context.Background()
	pairRace(t, c, svc, func(_, in, _ string) func() error {
		var cents int64
		if err := c.Pool.QueryRow(ctx, `SELECT round(amount * 100)::bigint FROM bank_import_rows WHERE id = $1`, in).Scan(&cents); err != nil {
			t.Fatalf("read row: %v", err)
		}
		inv := c.NewInvoice(t, fin.InvoiceOpts{Cents: cents})
		return func() error {
			_, err := svc.AssignBankRowToInvoice(ctx, c.ID, c.Treasurer.ID, in, inv.ID)
			return err
		}
	}, func(err error) bool { return errors.Is(err, errBankRowRaced) })
}

// runBoth runs a and b at once; on odd rounds b starts a little later, so
// both orders are exercised.
func runBoth(round int, a, b func() error) (errA, errB error) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); errA = a() }()
	go func() {
		defer wg.Done()
		if round%2 == 1 {
			time.Sleep(time.Duration(10*round) * time.Millisecond)
		}
		errB = b()
	}()
	wg.Wait()
	return errA, errB
}

func noDeadlock(t *testing.T, round int, errs ...error) {
	t.Helper()
	for _, err := range errs {
		if err != nil && strings.Contains(err.Error(), "40P01") {
			t.Fatalf("round %d: deadlock: %v", round, err)
		}
	}
}

// rowEntries counts the entries a bank row is linked to or sourced from that
// are not voided (a discarded loser leaves none).
func rowEntries(t *testing.T, c *fin.Club, rowID string) int {
	t.Helper()
	var n int
	if err := c.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM journal_entries WHERE club_id = $1 AND source_id = $2 AND status <> 'voided'`,
		c.ID, rowID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The KID cascade (faktura A) and a manual assign of the same row to another
// faktura (B) on a pool of two connections: one booking, a clean refusal.
func TestKIDCascadeAndManualAssignOfOneRowBookOnce(t *testing.T) {
	svc, c := newStickyClub(t, testutil.WithMaxConns(2))
	ctx := context.Background()
	for i := range 6 {
		date := day(c.Year, 8, 1+i)
		cents := int64(31000 + i)
		a := c.NewInvoice(t, fin.InvoiceOpts{Cents: cents})
		b := c.NewInvoice(t, fin.InvoiceOpts{Cents: cents})
		row := c.NewBankRow(t, c.NewBankImportOn(t, "1920"), fin.BankRowOpts{Date: date, Cents: cents})
		errKID, errAssign := runBoth(i, func() error {
			return svc.bookKIDCascadeMatch(ctx, c.ID, kidCascadeMatch{
				rowID: row, invoiceID: a.ID, actorID: c.Treasurer.ID, bankAccount: "1920",
				kid: a.KID, desc: "Synthetic", date: date, amount: fin.Kr(cents),
			})
		}, func() error {
			_, err := svc.AssignBankRowToInvoice(ctx, c.ID, c.Treasurer.ID, row, b.ID)
			return err
		})
		noDeadlock(t, i, errKID, errAssign)
		if (errKID == nil) == (errAssign == nil) {
			t.Fatalf("round %d: KID err %v, assign err %v; want exactly one to book", i, errKID, errAssign)
		}
		if errKID != nil && !errors.Is(errKID, errKIDRowTaken) {
			t.Fatalf("round %d: KID cascade lost with %v, want errKIDRowTaken", i, errKID)
		}
		if errAssign != nil && !errors.Is(errAssign, errBankRowRaced) {
			t.Fatalf("round %d: assign lost with %v, want errBankRowRaced", i, errAssign)
		}
		if n := rowEntries(t, c, row); n != 1 {
			t.Fatalf("round %d: %d entries for the row, want 1", i, n)
		}
	}
}

// PairRefund and a manual account assign of its outbound row on a pool of
// two connections: one booking of the outbound row, a clean refusal.
func TestPairRefundAndAccountAssignBookOnce(t *testing.T) {
	svc, c := newStickyClub(t, testutil.WithMaxConns(2))
	ctx := context.Background()
	for i := range 6 {
		date := day(c.Year, 9, 1+i)
		cents := int64(42000 + i)
		imp := c.NewBankImportOn(t, "1920")
		in := c.NewBankRow(t, imp, fin.BankRowOpts{Date: date, Cents: cents})
		out := c.NewBankRow(t, imp, fin.BankRowOpts{Date: date, Cents: -cents})
		if err := svc.DismissBankRow(ctx, c.ID, c.Treasurer.ID, in, "double_payment"); err != nil {
			t.Fatalf("dismiss: %v", err)
		}
		errPair, errAssign := runBoth(i, func() error {
			_, _, err := svc.PairRefund(ctx, c.ID, c.Treasurer.ID, in, out)
			return err
		}, func() error {
			_, err := svc.AssignBankRowToAccount(ctx, c.ID, c.Treasurer.ID, out, "6200", "expense", "")
			return err
		})
		noDeadlock(t, i, errPair, errAssign)
		if (errPair == nil) == (errAssign == nil) {
			t.Fatalf("round %d: pair err %v, assign err %v; want exactly one to book", i, errPair, errAssign)
		}
		if errAssign != nil && !errors.Is(errAssign, errBankRowRaced) {
			t.Fatalf("round %d: assign lost with %v, want errBankRowRaced", i, errAssign)
		}
		if n := rowEntries(t, c, out); n != 1 {
			t.Fatalf("round %d: %d entries for the outbound row, want 1", i, n)
		}
	}
}

// lockWaiters polls until n backends of the database wait on a lock.
func lockWaiters(t *testing.T, c *fin.Club, n int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var got int
		if err := c.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`,
		).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("fewer than %d backends waiting on a lock after 30 s", n)
}

// Deterministic: the faktura assign reaches the incoming row first (it holds
// the faktura and queues on the row), the pair queues behind it. Released,
// the assign books and the pair is refused as row_booked, with no deadlock
// on the bilag number and nothing kept.
func TestPairBankTransferQueuedBehindAFakturaAssign(t *testing.T) {
	svc, c := newStickyClub(t, testutil.WithMaxConns(8)) // the holder, both bookings and the polls
	out, in, _ := transferPairRows(t, c, 47000, day(c.Year, 10, 1), day(c.Year, 10, 1))
	inv := c.NewInvoice(t, fin.InvoiceOpts{Cents: 47000})
	lock := testutil.HoldRowLock(t, c.Pool, `SELECT id FROM bank_import_rows WHERE id = $1 FOR UPDATE`, in)
	defer lock.Release()

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	assignDone := make(chan error, 1)
	go func() {
		_, err := svc.AssignBankRowToInvoice(ctx, c.ID, c.Treasurer.ID, in, inv.ID)
		assignDone <- err
	}()
	lockWaiters(t, c, 1)
	pairDone := make(chan error, 1)
	go func() {
		_, err := svc.PairBankTransfer(ctx, c.ID, c.Treasurer.ID, []string{out, in}, "")
		pairDone <- err
	}()
	lockWaiters(t, c, 2)
	entries := clubEntries(t, c)
	lock.Release()

	if err := <-assignDone; err != nil {
		t.Fatalf("assign: %v", err)
	}
	var refusal *TransferRefusalError
	if err := <-pairDone; !errors.As(err, &refusal) || refusal.Code != TransferRowBooked {
		t.Fatalf("pair err = %v, want row_booked", err)
	}
	if got := clubEntries(t, c) - entries; got != 1 {
		t.Fatalf("%d new entries, want the assign's only", got)
	}
	if s := readTransferRow(t, c, out); s.entry != "" {
		t.Errorf("the outgoing row was booked: %+v", s)
	}
}

// Two account assigns at once on a pool of two connections both complete:
// each books on its own transaction and never waits for a second
// connection while it holds the import or its row (BRY-433).
func TestConcurrentAccountAssignsOnSmallPool(t *testing.T) {
	svc, c := newStickyClub(t, testutil.WithMaxConns(2))
	imp := c.NewBankImportOn(t, "1920")
	for i := range 4 {
		a := c.NewBankRow(t, imp, fin.BankRowOpts{Date: day(c.Year, 11, 1+i), Cents: -int64(1000 + i)})
		b := c.NewBankRow(t, imp, fin.BankRowOpts{Date: day(c.Year, 11, 1+i), Cents: -int64(2000 + i)})
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		errA, errB := runBoth(i, func() error {
			_, err := svc.AssignBankRowToAccount(ctx, c.ID, c.Treasurer.ID, a, "6200", "expense", "")
			return err
		}, func() error {
			_, err := svc.AssignBankRowToAccount(ctx, c.ID, c.Treasurer.ID, b, "6200", "expense", "")
			return err
		})
		cancel()
		if errA != nil || errB != nil {
			t.Fatalf("round %d: assigns err %v / %v; want both booked", i, errA, errB)
		}
		// The same row twice: one books, the other is refused.
		ctx, cancel = context.WithTimeout(t.Context(), 30*time.Second)
		row := c.NewBankRow(t, imp, fin.BankRowOpts{Date: day(c.Year, 11, 10+i), Cents: -int64(3000 + i)})
		errA, errB = runBoth(i, func() error {
			_, err := svc.AssignBankRowToAccount(ctx, c.ID, c.Treasurer.ID, row, "6200", "expense", "")
			return err
		}, func() error {
			_, err := svc.AssignBankRowToAccount(ctx, c.ID, c.Treasurer.ID, row, "6200", "expense", "")
			return err
		})
		cancel()
		noDeadlock(t, i, errA, errB)
		if (errA == nil) == (errB == nil) || (errA != nil && !errors.Is(errA, errBankRowRaced)) || (errB != nil && !errors.Is(errB, errBankRowRaced)) {
			t.Fatalf("round %d: same-row assigns err %v / %v; want one booked, one errBankRowRaced", i, errA, errB)
		}
		if n := rowEntries(t, c, row); n != 1 {
			t.Fatalf("round %d: %d entries for the row, want 1", i, n)
		}
	}
}

// A multi-row faktura assign that names one row twice (in any spelling) is
// refused before anything is booked: each named row gets its own entry.
func TestMultiRowInvoiceAssignRefusesADuplicateRow(t *testing.T) {
	svc, c := newStickyClub(t)
	ctx := context.Background()
	inv := c.NewInvoice(t, fin.InvoiceOpts{Cents: 20000})
	imp := c.NewBankImportOn(t, "1920")
	a := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 10000})
	entries := clubEntries(t, c)
	for _, ids := range [][]string{{a, a}, {a, strings.ToUpper(a)}} {
		if _, err := svc.AssignMultipleBankRowsToInvoice(ctx, c.ID, c.Treasurer.ID, ids, inv.ID); !errors.Is(err, ErrDuplicateBankRows) {
			t.Errorf("%v: err = %v, want ErrDuplicateBankRows", ids, err)
		}
	}
	if clubEntries(t, c) != entries || readTransferRow(t, c, a).entry != "" {
		t.Error("a refused duplicate assign booked something")
	}
}

// Angre on a person's transfer voids it and declines both rows again, so the
// next sync leaves them for a person.
func TestPairBankTransferUndoDeclinesBothAgain(t *testing.T) {
	svc, c := newStickyClub(t)
	ctx := context.Background()
	out, in, _ := transferPairRows(t, c, 60000, day(c.Year, 8, 1), day(c.Year, 8, 1))
	res, err := svc.PairBankTransfer(ctx, c.ID, c.Treasurer.ID, []string{out, in}, "")
	if err != nil {
		t.Fatalf("PairBankTransfer: %v", err)
	}
	un, err := svc.UnassignBankRowReported(ctx, c.ID, c.Treasurer.ID, in)
	if err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if !slices.Contains(un.SiblingRowIDs, out) {
		t.Errorf("unassign siblings = %v; want the other side released too", un.SiblingRowIDs)
	}
	if e := readTransferEntry(t, c, res.JournalEntryID); e.status != "voided" {
		t.Errorf("entry status %q, want voided", e.status)
	}
	for _, id := range []string{out, in} {
		if s := readTransferRow(t, c, id); s.entry != "" || !s.declined {
			t.Errorf("row %s = %+v; want unbooked and declined", id, s)
		}
	}
	s, err := svc.BankSync(ctx, c.ID, c.Treasurer.ID)
	if err != nil {
		t.Fatalf("BankSync: %v", err)
	}
	if s.TransfersLinked != 0 {
		t.Errorf("sync after the undo linked %d transfers; want 0", s.TransfersLinked)
	}
}
