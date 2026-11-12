package accounting

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// BRY-669: the person paths that may open a fiscal year (manual Vipps: Lag
// bilag, Søk faktura, the backlog rebook; the refund pair) refuse a period
// that is not open before they create an entry, and a locked period with its
// own reason instead of the entry insert's generic ErrPeriodClosed. PairRefund
// books its pair in one transaction.

type manualPeriodFixture struct {
	svc  *Service
	club *fin.Club
	imp  string
	day  time.Time
}

func newManualPeriodFixture(t *testing.T, opts ...testutil.DBOption) *manualPeriodFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t, opts...)
	svc := NewService(pool, audit.NewService(pool, zerolog.Nop()), zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	f := &manualPeriodFixture{svc: svc, club: club, imp: club.NewBankImport(t), day: time.Date(club.Year, 6, 2, 0, 0, 0, 0, time.UTC)}
	f.exec(t, `UPDATE clubs SET vipps_per_charge_enabled = true WHERE id = $1`, club.ID)
	return f
}

func (f *manualPeriodFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.club.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func (f *manualPeriodFixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

// setPeriod sets the club's own fiscal period to status.
func (f *manualPeriodFixture) setPeriod(t *testing.T, status string) {
	t.Helper()
	f.exec(t,
		`UPDATE fiscal_periods SET status = $2::fiscal_period_status,
		        closed_at = CASE WHEN $2 = 'open' THEN NULL ELSE now() END
		  WHERE id = $1`, f.club.PeriodID, status)
}

func (f *manualPeriodFixture) entries(t *testing.T) int {
	return f.count(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID)
}

func (f *manualPeriodFixture) bankEntry(t *testing.T, rowID string) string {
	t.Helper()
	var id *string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT journal_entry_id::text FROM bank_import_rows WHERE id = $1`, rowID).Scan(&id); err != nil {
		t.Fatalf("bank row %s: %v", rowID, err)
	}
	if id == nil {
		return ""
	}
	return *id
}

// refundPair seeds an incoming row dismissed as a double payment and the
// outgoing transfer that refunds it, a day later.
func (f *manualPeriodFixture) refundPair(t *testing.T, cents int64) (inbound, outbound string) {
	t.Helper()
	inbound = f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: f.day, Cents: cents, Counterpart: "Synthetic Payer", Description: "Synthetic double payment"})
	outbound = f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: f.day.AddDate(0, 0, 1), Cents: -cents, Counterpart: "Synthetic Payer", Description: "Synthetic refund"})
	if err := f.svc.DismissBankRow(context.Background(), f.club.ID, f.club.Treasurer.ID, inbound, "double_payment"); err != nil {
		t.Fatalf("dismiss inbound: %v", err)
	}
	return inbound, outbound
}

// wantNotOpenRefusal checks a refusal's text: a closed period keeps the
// reopen wording, a locked one says it cannot be reopened.
func wantNotOpenRefusal(t *testing.T, what string, err error, year int, status string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s in a %s period: booked, want a refusal", what, status)
	}
	if errors.Is(err, ErrPeriodClosed) {
		t.Errorf("%s: err = %v, want the refusal before the entry, not the insert's ErrPeriodClosed", what, err)
	}
	want := periodNotOpenConfirmError(year, status).Error()
	if err.Error() != want {
		t.Errorf("%s: err = %q, want %q", what, err.Error(), want)
	}
	if status == "locked" && !strings.Contains(err.Error(), "locked") {
		t.Errorf("%s: a locked period is refused without saying so: %q", what, err.Error())
	}
}

func TestCreateVippsChargeBilagRefusesNotOpenPeriodWithItsOwnReason(t *testing.T) {
	for _, status := range []string{"closed", "locked"} {
		t.Run(status, func(t *testing.T) {
			f := newManualPeriodFixture(t)
			charge := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, Date: f.day, CustomerName: "Synthetic Walkup"})
			f.setPeriod(t, status)
			before := f.entries(t)

			_, err := f.svc.CreateVippsChargeBilag(context.Background(), f.club.ID, charge, "3900", "", f.club.Treasurer.ID)
			wantNotOpenRefusal(t, "Lag bilag", err, f.club.Year, status)
			if f.entries(t) != before || rowEntry(t, f.club, charge) != "" {
				t.Error("a refused Lag bilag left an entry or booked the charge")
			}
		})
	}
}

func TestAssignVippsChargeToInvoiceRefusesNotOpenPeriod(t *testing.T) {
	for _, status := range []string{"closed", "locked"} {
		t.Run(status, func(t *testing.T) {
			f := newManualPeriodFixture(t)
			inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, IssueDate: f.day.AddDate(0, -1, 0)})
			charge := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, Date: f.day, CustomerName: "Synthetic Payer"})
			f.setPeriod(t, status)
			before := f.entries(t)

			_, err := f.svc.AssignVippsChargeToInvoice(context.Background(), f.club.ID, charge, inv.ID, f.club.Treasurer.ID)
			wantNotOpenRefusal(t, "Søk faktura", err, f.club.Year, status)
			if f.entries(t) != before || rowEntry(t, f.club, charge) != "" {
				t.Error("a refused faktura assign left an entry or booked the charge")
			}
			if f.count(t, `SELECT count(*) FROM invoices WHERE id = $1 AND payment_id IS NOT NULL`, inv.ID) != 0 {
				t.Error("a refused faktura assign paid the faktura")
			}
		})
	}
}

// The supervised backlog rebook books through postChargeTx with
// periodMayCreate too. Its charge is on a draft here, so the old booking is
// deleted rather than voided and the replacement reaches the period check.
func TestRebookVippsChargeRefusesLockedPeriod(t *testing.T) {
	f := newManualPeriodFixture(t)
	ctx := context.Background()
	charge := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, Date: f.day, CustomerName: "Synthetic Walkup"})
	booked, err := f.svc.CreateVippsChargeBilag(ctx, f.club.ID, charge, "3900", "", f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("booking the charge: %v", err)
	}
	f.exec(t, `UPDATE journal_entries SET status = 'draft', posted_at = NULL, posted_by = NULL WHERE id = $1`, booked.EntryID)
	f.setPeriod(t, "locked")

	tx, err := f.club.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = rebookVippsChargeTx(ctx, tx, f.club.ID, f.club.Treasurer.ID, charge,
		[]CreateJournalLineInput{{AccountCode: "3200", Credit: 300, Description: "Synthetic rebooking"}}, "")
	wantNotOpenRefusal(t, "rebook", err, f.club.Year, "locked")
}

func TestPairRefundRefusesNotOpenPeriod(t *testing.T) {
	for _, status := range []string{"closed", "locked"} {
		t.Run(status, func(t *testing.T) {
			f := newManualPeriodFixture(t)
			inbound, outbound := f.refundPair(t, 25000)
			f.setPeriod(t, status)
			before := f.entries(t)

			_, _, err := f.svc.PairRefund(context.Background(), f.club.ID, f.club.Treasurer.ID, inbound, outbound)
			wantNotOpenRefusal(t, "PairRefund", err, f.club.Year, status)
			if f.entries(t) != before || f.bankEntry(t, inbound) != "" || f.bankEntry(t, outbound) != "" {
				t.Error("a refused refund pair left an entry or linked a row")
			}
			if n := f.count(t, `SELECT count(*) FROM bank_import_rows WHERE id IN ($1, $2) AND refund_paired_with IS NOT NULL`, inbound, outbound); n != 0 {
				t.Errorf("%d rows cross-linked by a refused pair", n)
			}
		})
	}
}

// Regression: a refund pair books two posted, balanced entries on the
// outbound date, links and cross-links both rows, keeps the inbound row
// dismissed and lifts a person's decline. The refusals are unchanged.
func TestPairRefundBooksAndCrossLinksBothRows(t *testing.T) {
	f := newManualPeriodFixture(t)
	ctx := context.Background()
	inbound, outbound := f.refundPair(t, 25000)
	f.exec(t, `UPDATE bank_import_rows SET auto_declined_at = now() WHERE id = $1`, outbound)

	inEntry, outEntry, err := f.svc.PairRefund(ctx, f.club.ID, f.club.Treasurer.ID, inbound, outbound)
	if err != nil {
		t.Fatalf("PairRefund: %v", err)
	}
	for name, e := range map[string]string{"inbound": inEntry, "outbound": outEntry} {
		je, err := f.svc.GetJournalEntry(ctx, f.club.ID, e)
		if err != nil {
			t.Fatalf("%s entry: %v", name, err)
		}
		if je.Status != "posted" || je.EntryDate != f.day.AddDate(0, 0, 1).Format("2006-01-02") || je.FiscalPeriodID != f.club.PeriodID {
			t.Errorf("%s entry = status %s, date %s, period %s; want posted on the outbound date in the club's period",
				name, je.Status, je.EntryDate, je.FiscalPeriodID)
		}
		var debit, credit float64
		for _, l := range je.Lines {
			debit += l.Debit
			credit += l.Credit
		}
		if debit != 250 || credit != 250 {
			t.Errorf("%s entry debits %.2f, credits %.2f; want 250 each", name, debit, credit)
		}
	}
	type rowState struct {
		entry, paired   string
		dismissed, decl bool
	}
	read := func(id string) rowState {
		var r rowState
		var entry, paired *string
		if err := f.club.Pool.QueryRow(ctx,
			`SELECT journal_entry_id::text, refund_paired_with::text, dismissed_at IS NOT NULL, auto_declined_at IS NOT NULL
			   FROM bank_import_rows WHERE id = $1`, id).Scan(&entry, &paired, &r.dismissed, &r.decl); err != nil {
			t.Fatalf("row %s: %v", id, err)
		}
		if entry != nil {
			r.entry = *entry
		}
		if paired != nil {
			r.paired = *paired
		}
		return r
	}
	if in := read(inbound); in.entry != inEntry || in.paired != outbound || !in.dismissed {
		t.Errorf("inbound = %+v; want linked to %s, paired with the outbound row, still dismissed", in, inEntry)
	}
	if out := read(outbound); out.entry != outEntry || out.paired != inbound || out.decl {
		t.Errorf("outbound = %+v; want linked to %s, paired with the inbound row, decline lifted", out, outEntry)
	}

	// Pairing again, or pairing the booked outbound row with another
	// pending refund, is refused and books nothing.
	before := f.entries(t)
	if _, _, err := f.svc.PairRefund(ctx, f.club.ID, f.club.Treasurer.ID, inbound, outbound); err == nil {
		t.Error("pairing a paired refund again succeeded")
	}
	other, _ := f.refundPair(t, 25000)
	if _, _, err := f.svc.PairRefund(ctx, f.club.ID, f.club.Treasurer.ID, other, outbound); err == nil ||
		!strings.Contains(err.Error(), "outbound row is already reconciled") {
		t.Errorf("pairing a booked outbound row: err = %v, want it refused as reconciled", err)
	}
	notDismissed := f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: f.day, Cents: 25000, Description: "Synthetic deposit"})
	_, freshOut := f.refundPair(t, 25000)
	if _, _, err := f.svc.PairRefund(ctx, f.club.ID, f.club.Treasurer.ID, notDismissed, freshOut); err == nil ||
		!strings.Contains(err.Error(), "must be dismissed with a refund reason") {
		t.Errorf("pairing an undismissed inbound row: err = %v, want the refund-reason refusal", err)
	}
	if _, _, err := f.svc.PairRefund(ctx, f.club.ID, f.club.Treasurer.ID, other, other); err == nil {
		t.Error("pairing a row with itself succeeded")
	}
	if f.entries(t) != before {
		t.Errorf("refused pairs left %d entries", f.entries(t)-before)
	}
}

// A person assigning the outbound row to an account while another pairs it
// as a refund: on a two-connection pool, exactly one of them books the row,
// the other is refused, no draft is left and nothing waits out the deadline
// (PairRefund holds its row locks on one connection and never asks the pool
// for a second).
func TestPairRefundRacesSingleRowAssign(t *testing.T) {
	f := newManualPeriodFixture(t, testutil.WithMaxConns(2))
	const rounds = 6
	pairWins, assignWins := 0, 0
	for i := range rounds {
		inbound, outbound := f.refundPair(t, int64(20000+i*100))

		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		var wg sync.WaitGroup
		start := make(chan struct{})
		var pairErr, assignErr error
		var outEntry, assignEntry string
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, outEntry, pairErr = f.svc.PairRefund(ctx, f.club.ID, f.club.Treasurer.ID, inbound, outbound)
		}()
		go func() {
			defer wg.Done()
			<-start
			assignEntry, assignErr = f.svc.AssignBankRowToAccount(ctx, f.club.ID, f.club.Treasurer.ID, outbound, "6100", "expense", "")
		}()
		close(start)
		wg.Wait()
		deadline := ctx.Err()
		cancel()
		if deadline != nil || errors.Is(pairErr, context.DeadlineExceeded) || errors.Is(assignErr, context.DeadlineExceeded) {
			t.Fatalf("round %d hit the deadline: pair %v, assign %v", i, pairErr, assignErr)
		}

		booked := f.bankEntry(t, outbound)
		switch {
		case pairErr == nil && assignErr == nil:
			t.Fatalf("round %d: both booked the outbound row (pair %s, assign %s)", i, outEntry, assignEntry)
		case pairErr == nil:
			pairWins++
			if booked != outEntry {
				t.Errorf("round %d: outbound row on %s, want the pair's %s", i, booked, outEntry)
			}
			if !errors.Is(assignErr, errBankRowRaced) {
				t.Errorf("round %d: the losing assign: %v, want errBankRowRaced", i, assignErr)
			}
		case assignErr == nil:
			assignWins++
			if booked != assignEntry {
				t.Errorf("round %d: outbound row on %s, want the assign's %s", i, booked, assignEntry)
			}
			if f.bankEntry(t, inbound) != "" {
				t.Errorf("round %d: the losing pair linked the inbound row", i)
			}
			if !errors.Is(pairErr, errBankRowRaced) && !strings.Contains(pairErr.Error(), "outbound row is already reconciled") {
				t.Errorf("round %d: the losing pair: %v, want a refusal", i, pairErr)
			}
		default:
			t.Fatalf("round %d: neither booked: pair %v, assign %v", i, pairErr, assignErr)
		}
		if n := f.count(t,
			`SELECT count(*) FROM journal_entries WHERE club_id = $1 AND source_id = $2 AND status <> 'voided'`,
			f.club.ID, outbound); n != 1 {
			t.Errorf("round %d: %d entries for the outbound row, want 1", i, n)
		}
		if n := f.count(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1 AND status = 'draft'`, f.club.ID); n != 0 {
			t.Errorf("round %d: %d drafts left behind", i, n)
		}
	}
	t.Logf("pair won %d, assign won %d of %d rounds", pairWins, assignWins, rounds)
}

// A refund pair returns the exact amount: an outbound row that differs by
// an øre is refused (both entries would book the inbound amount and leave the
// difference on the bank account), and only exact amounts are suggested. A
// provisional outbound row is neither suggested nor paired, and an inbound
// row that is already booked is refused.
func TestPairRefundRefusesWhatItCannotBookCleanly(t *testing.T) {
	f := newManualPeriodFixture(t)
	ctx := context.Background()
	inbound, _ := f.refundPair(t, 25000)
	out := func(cents int64, desc string) string {
		return f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: f.day.AddDate(0, 0, 2), Cents: -cents, Counterpart: "Synthetic Payer", Description: desc})
	}
	under, over := out(24999, "Synthetic refund short"), out(25100, "Synthetic refund long")

	// A provisional outgoing row of the exact amount: a bare transfer text,
	// no counterpart or KID, dated on its own statement's last day.
	provImp := f.club.NewBankImport(t)
	prov := f.club.NewBankRow(t, provImp, fin.BankRowOpts{Date: f.day.AddDate(0, 0, 3), Cents: -25000, Description: "Overførsel"})
	f.exec(t, `UPDATE bank_import_rows SET counterpart = '', kid_number = '' WHERE id = $1`, prov)
	f.exec(t, `UPDATE bank_imports SET period_to = $2 WHERE id = $1`, provImp, f.day.AddDate(0, 0, 3))

	sugg, err := f.svc.SuggestRefundOutbound(ctx, f.club.ID, inbound)
	if err != nil {
		t.Fatalf("SuggestRefundOutbound: %v", err)
	}
	var ids []string
	for _, c := range sugg {
		ids = append(ids, c.ID)
	}
	// The fixture's own outbound row (refundPair) is the one exact match.
	if len(ids) != 1 || ids[0] == under || ids[0] == over || ids[0] == prov {
		t.Errorf("suggestions = %v; want only the exact, settled outbound row", ids)
	}

	before := f.entries(t)
	for name, row := range map[string]string{"short": under, "long": over} {
		_, _, err := f.svc.PairRefund(ctx, f.club.ID, f.club.Treasurer.ID, inbound, row)
		if err == nil || !strings.Contains(err.Error(), "does not match the inbound amount") {
			t.Errorf("%s outbound: err = %v, want the amount refusal", name, err)
		}
	}
	if _, _, err := f.svc.PairRefund(ctx, f.club.ID, f.club.Treasurer.ID, inbound, prov); err == nil ||
		!strings.Contains(err.Error(), "provisional") {
		t.Errorf("provisional outbound: err = %v, want the provisional refusal", err)
	}

	// An inbound row already linked to an entry (state no path should leave,
	// but the pair must not overwrite the link).
	other := f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: f.day, Cents: 1000, Description: "Synthetic other"})
	entry, err := f.svc.AssignBankRowToAccount(ctx, f.club.ID, f.club.Treasurer.ID, other, "3900", "revenue", "")
	if err != nil {
		t.Fatalf("booking a row: %v", err)
	}
	booked, bookedOut := f.refundPair(t, 26000)
	f.exec(t, `UPDATE bank_import_rows SET journal_entry_id = $2 WHERE id = $1`, booked, entry)
	before++
	if _, _, err := f.svc.PairRefund(ctx, f.club.ID, f.club.Treasurer.ID, booked, bookedOut); err == nil ||
		!strings.Contains(err.Error(), "inbound row is already reconciled") {
		t.Errorf("booked inbound: err = %v, want it refused as reconciled", err)
	}
	if f.entries(t) != before {
		t.Errorf("refused pairs left %d entries", f.entries(t)-before)
	}
}

// PairRefund may open the outbound date's year (a person's pairing), but a
// pair refused after that rolls the new year back with everything else.
func TestPairRefundRefusedLeavesNoNewYear(t *testing.T) {
	f := newManualPeriodFixture(t)
	ctx := context.Background()
	next := f.club.Year + 1
	periods := func() int {
		return f.count(t, `SELECT count(*) FROM fiscal_periods WHERE club_id = $1 AND year = $2`, f.club.ID, next)
	}
	newYear := time.Date(next, 1, 5, 0, 0, 0, 0, time.UTC)

	// Refused up front: the amounts differ.
	inbound, _ := f.refundPair(t, 25000)
	short := f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Date: newYear, Cents: -24000, Description: "Synthetic refund"})
	if _, _, err := f.svc.PairRefund(ctx, f.club.ID, f.club.Treasurer.ID, inbound, short); err == nil {
		t.Fatal("a short refund was paired")
	}

	// Refused after the year was resolved on the transaction: the bank
	// account the entries would post to is not in the chart of accounts.
	badImp := f.club.NewBankImportOn(t, "1999")
	in2 := f.club.NewBankRow(t, badImp, fin.BankRowOpts{Date: f.day, Cents: 27000, Description: "Synthetic double payment"})
	if err := f.svc.DismissBankRow(ctx, f.club.ID, f.club.Treasurer.ID, in2, "double_payment"); err != nil {
		t.Fatal(err)
	}
	out2 := f.club.NewBankRow(t, badImp, fin.BankRowOpts{Date: newYear, Cents: -27000, Description: "Synthetic refund"})
	if _, _, err := f.svc.PairRefund(ctx, f.club.ID, f.club.Treasurer.ID, in2, out2); err == nil ||
		!strings.Contains(err.Error(), "account 1999 not found") {
		t.Fatalf("pair on an unknown bank account: err = %v, want the entry refused after the period resolve", err)
	}
	if n := periods(); n != 0 {
		t.Errorf("%d fiscal periods for %d after refused pairs; want the new year rolled back", n, next)
	}
	if f.bankEntry(t, in2) != "" || f.bankEntry(t, out2) != "" {
		t.Error("a refused pair linked a row")
	}
}

// waitForLockWaiters polls until n backends of the database wait on a lock.
func waitForLockWaiters(t *testing.T, f *manualPeriodFixture, n int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if f.count(t, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("fewer than %d backends waiting on a lock after 30 s", n)
}

// The ordering the old PairRefund lost: the assign takes the outbound row
// first and the pair arrives while it is still booking. The pair must see the
// booked row and refuse, never overwrite the link (which left two posted
// entries for one bank row).
func TestPairRefundLosesToAnAssignThatHoldsTheRow(t *testing.T) {
	f := newManualPeriodFixture(t, testutil.WithMaxConns(8)) // the holder, both bookings and the polls
	inbound, outbound := f.refundPair(t, 31000)
	lock := testutil.HoldRowLock(t, f.club.Pool, `SELECT id FROM bank_import_rows WHERE id = $1 FOR UPDATE`, outbound)
	defer lock.Release()

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	assignDone := make(chan error, 1)
	var assignEntry string
	go func() {
		var err error
		assignEntry, err = f.svc.AssignBankRowToAccount(ctx, f.club.ID, f.club.Treasurer.ID, outbound, "6100", "expense", "")
		assignDone <- err
	}()
	waitForLockWaiters(t, f, 1)
	pairDone := make(chan error, 1)
	go func() {
		_, _, err := f.svc.PairRefund(ctx, f.club.ID, f.club.Treasurer.ID, inbound, outbound)
		pairDone <- err
	}()
	waitForLockWaiters(t, f, 2)
	lock.Release()

	if err := <-assignDone; err != nil {
		t.Fatalf("assign: %v", err)
	}
	pairErr := <-pairDone
	if pairErr == nil {
		t.Fatal("the pair booked a row the assign had booked")
	}
	if !errors.Is(pairErr, errBankRowRaced) && !strings.Contains(pairErr.Error(), "outbound row is already reconciled") {
		t.Errorf("pair: %v, want a refusal", pairErr)
	}
	if got := f.bankEntry(t, outbound); got != assignEntry {
		t.Errorf("outbound row on %s, want the assign's %s", got, assignEntry)
	}
	if f.bankEntry(t, inbound) != "" {
		t.Error("the refused pair linked the inbound row")
	}
	if n := f.count(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1 AND source_id IN ($2, $3) AND status <> 'voided'`,
		f.club.ID, inbound, outbound); n != 1 {
		t.Errorf("%d entries for the pair's rows, want only the assign's", n)
	}
}
