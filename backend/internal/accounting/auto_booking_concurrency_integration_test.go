package accounting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

// Tests of the Vipps booking slot (BRY-564): what a held slot does to a
// person's request, the bounded waits, the sweep on bank upload, and that
// correctness never depends on the slot.

type slotFixture struct {
	svc      *Service
	club     *fin.Club
	bankImp  string
	priceSeq int
}

// newSlotFixture is a per-charge club with both automatic rules on and one
// price item whose amount (1600 kr) is unique, so a charge of that amount
// books by the price-item rule.
func newSlotFixture(t *testing.T, opts ...testutil.DBOption) *slotFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t, opts...)
	svc := NewService(pool, audit.NewService(pool, zerolog.Nop()), zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	f := &slotFixture{svc: svc, club: club, bankImp: club.NewBankImport(t)}
	f.exec(t, `UPDATE clubs SET vipps_per_charge_enabled = true, vipps_auto_assign = true, bank_auto_assign = true WHERE id = $1`, club.ID)
	f.exec(t,
		`INSERT INTO price_items (club_id, category, name, amount, unit, is_active, audience)
		 VALUES ($1, 'service', 'Synthetic slipping', 1600, 'once', true, 'non_member')`, club.ID)
	return f
}

func (f *slotFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.club.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func (f *slotFixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

// slotSettlement is one synthetic settlement: its charges, a fee, the payout
// row and the bank deposit naming it.
type slotSettlement struct {
	number  string
	booking time.Time
	charges []string
	order   *fin.Invoice
	bankRow string
}

// settlement seeds priceCharges charges the price-item rule books, plus an
// order-linked charge when withOrder, a fee, the payout row and the deposit.
func (f *slotFixture) settlement(t *testing.T, number string, day, priceCharges int, withOrder bool) slotSettlement {
	t.Helper()
	s := slotSettlement{number: number, booking: time.Date(f.club.Year, 6, day, 0, 0, 0, 0, time.UTC)}
	var gross int64
	for range priceCharges {
		f.priceSeq++
		s.charges = append(s.charges, f.club.NewVippsRow(t, fin.VippsRowOpts{
			Cents: 160000, Date: s.booking, MSN: sweepMSN,
			CustomerName: fmt.Sprintf("Synthetic Walk-in %d", f.priceSeq),
		}))
		gross += 160000
	}
	if withOrder {
		inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
		order := f.club.NewOrderForInvoice(t, inv)
		s.charges = append(s.charges, f.club.NewVippsRow(t, fin.VippsRowOpts{
			Cents: 45000, Date: s.booking, MSN: sweepMSN, OrderID: order.VippsReference,
		}))
		s.order = &inv
		gross += 45000
	}
	const fee = 5000
	f.club.NewVippsRow(t, fin.VippsRowOpts{RowType: "fee", Cents: -fee, Date: s.booking, MSN: sweepMSN})
	f.club.NewVippsRow(t, fin.VippsRowOpts{RowType: "payout", Cents: -(gross - fee), Date: s.booking, MSN: sweepMSN, Settlement: number})
	s.bankRow = f.club.NewBankRow(t, f.bankImp, fin.BankRowOpts{
		Date: s.booking.AddDate(0, 0, 1), Cents: gross - fee, Description: "Utb. " + number + " Vippsnr " + sweepMSN,
	})
	return s
}

func (f *slotFixture) entries(t *testing.T) int {
	t.Helper()
	return f.count(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID)
}

func (f *slotFixture) autoAssignAudits(t *testing.T) int {
	t.Helper()
	return f.count(t, `SELECT count(*) FROM audit_log WHERE club_id = $1 AND action = $2`, f.club.ID, audit.ActionVippsChargeAutoAssigned)
}

func (f *slotFixture) bookedCharges(t *testing.T, ss ...slotSettlement) int {
	t.Helper()
	n := 0
	for _, s := range ss {
		for _, c := range s.charges {
			if rowEntry(t, f.club, c) != "" {
				n++
			}
		}
	}
	return n
}

func (f *slotFixture) clearedDeposits(t *testing.T, ss ...slotSettlement) int {
	t.Helper()
	n := 0
	for _, s := range ss {
		if f.count(t, `SELECT count(*) FROM bank_import_rows WHERE id = $1 AND journal_entry_id IS NOT NULL`, s.bankRow) == 1 {
			n++
		}
	}
	return n
}

// holdSlot takes the club's Vipps booking slot as a long-running booking
// would, and gives it back at the end of the test at the latest.
func (f *slotFixture) holdSlot(t *testing.T) func() {
	t.Helper()
	free, ok := f.svc.vippsBooking.tryAcquire(f.club.ID)
	if !ok {
		t.Fatal("booking slot not free")
	}
	var once sync.Once
	release := func() { once.Do(free) }
	t.Cleanup(release)
	return release
}

// A person's Synk Vipps, another person's, and a long waiter standing in for
// the unattended run (it waits longer than a person) race on the same queue
// through the slot. Each charge is booked once, each settlement cleared once,
// and the auto-assign audit has one row per automatically booked charge.
func TestVippsBookingSlotSerialisesPeopleAndLongWaiter(t *testing.T) {
	f := newSlotFixture(t)
	a := f.settlement(t, "5000101", 2, 2, true)
	b := f.settlement(t, "5000102", 3, 2, false)
	other := f.club.NewMember(t, "treasurer")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	var (
		wg      sync.WaitGroup
		start   = make(chan struct{})
		mu      sync.Mutex
		orders  int
		assign  int
		cleared int
		errs    []error
	)
	note := func(o *VippsOrderReconcileResult, aa *AutoAssignResult, p *VippsPayoutSweepResult, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			errs = append(errs, err)
			return
		}
		if o != nil {
			orders += o.Booked
		}
		if aa != nil {
			assign += len(aa.Assigned)
		}
		if p != nil {
			cleared += len(p.Cleared)
		}
	}
	run := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			fn()
		}()
	}
	run(func() {
		r, err := f.svc.ResyncVippsBilags(ctx, f.club.ID, f.club.Treasurer.ID)
		if err != nil {
			note(nil, nil, nil, err)
			return
		}
		note(r.Orders, r.AutoAssign, r.Payouts, nil)
	})
	run(func() {
		r, err := f.svc.ResyncVippsBilags(ctx, f.club.ID, other.ID)
		if err != nil {
			note(nil, nil, nil, err)
			return
		}
		note(r.Orders, r.AutoAssign, r.Payouts, nil)
	})
	run(func() {
		r, err := f.svc.BookVippsQueue(ctx, f.club.ID, other.ID, 30*time.Second)
		if err != nil {
			note(nil, nil, nil, err)
			return
		}
		note(r.Orders, r.AutoAssign, r.Payouts, nil)
	})
	close(start)
	wg.Wait()

	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if orders != 1 || assign != 4 || cleared != 2 {
		t.Errorf("booked %d order charges, %d auto-assigned, cleared %d settlements; want 1, 4 and 2", orders, assign, cleared)
	}
	if n := f.bookedCharges(t, a, b); n != 5 {
		t.Errorf("%d of 5 charges booked", n)
	}
	if n := f.clearedDeposits(t, a, b); n != 2 {
		t.Errorf("%d of 2 deposits cleared", n)
	}
	// 5 charge entries, and a fee and a bank entry per settlement.
	if n := f.entries(t); n != 9 {
		t.Errorf("%d journal entries, want 9", n)
	}
	if n := f.autoAssignAudits(t); n != 4 {
		t.Errorf("%d auto_assigned audit rows, want 4 (one per automatically booked charge)", n)
	}
}

// While the slot is held a person's Synk Vipps waits its bound and then
// answers ErrVippsBookingBusy with nothing booked; a request that ends while
// waiting reports its own error, not busy; once the slot is free the same call
// books.
func TestVippsBookingSlotHeldMakesPeopleWaitThenBusy(t *testing.T) {
	f := newSlotFixture(t)
	s := f.settlement(t, "5000201", 2, 1, true)
	f.svc.vippsBookingSlotWait = 200 * time.Millisecond
	release := f.holdSlot(t)

	start := time.Now()
	_, err := f.svc.ResyncVippsBilags(context.Background(), f.club.ID, f.club.Treasurer.ID)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrVippsBookingBusy) {
		t.Fatalf("Synk Vipps while the slot is held: %v, want ErrVippsBookingBusy", err)
	}
	if elapsed < 150*time.Millisecond || elapsed > 5*time.Second {
		t.Errorf("busy after %s, want about the 200ms bound", elapsed)
	}
	if _, err := f.svc.BookVippsQueue(context.Background(), f.club.ID, f.club.Treasurer.ID, 0); !errors.Is(err, ErrVippsBookingBusy) {
		t.Errorf("BookVippsQueue while held: %v, want ErrVippsBookingBusy", err)
	}
	if n := f.bookedCharges(t, s); n != 0 || f.entries(t) != 0 {
		t.Errorf("a refused request booked %d charges and %d entries", n, f.entries(t))
	}

	gone, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	f.svc.vippsBookingSlotWait = time.Minute
	if _, err := f.svc.BookVippsQueue(gone, f.club.ID, f.club.Treasurer.ID, 0); !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrVippsBookingBusy) {
		t.Errorf("request ended while waiting: %v, want its own deadline error", err)
	}

	release()
	if _, err := f.svc.ResyncVippsBilags(context.Background(), f.club.ID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("Synk Vipps after release: %v", err)
	}
	if n := f.bookedCharges(t, s); n != 2 {
		t.Errorf("%d of 2 charges booked after release", n)
	}
}

// A longer wait than a person's is what the unattended run asks for: it gets
// the slot once the holder lets go, where a person gave up at their bound.
func TestVippsBookingSlotLongWaitOutlastsPersonsBound(t *testing.T) {
	f := newSlotFixture(t)
	s := f.settlement(t, "5000301", 2, 1, false)
	f.svc.vippsBookingSlotWait = 30 * time.Millisecond
	release := f.holdSlot(t)

	type out struct {
		res *VippsBookingResult
		err error
	}
	long := make(chan out, 1)
	go func() {
		res, err := f.svc.BookVippsQueue(context.Background(), f.club.ID, f.club.Treasurer.ID, 30*time.Second)
		long <- out{res, err}
	}()
	if _, err := f.svc.BookVippsQueue(context.Background(), f.club.ID, f.club.Treasurer.ID, 0); !errors.Is(err, ErrVippsBookingBusy) {
		t.Fatalf("person's wait: %v, want busy", err)
	}
	select {
	case o := <-long:
		t.Fatalf("long waiter returned while the slot was held: %+v", o)
	case <-time.After(150 * time.Millisecond):
	}
	release()
	select {
	case o := <-long:
		if o.err != nil {
			t.Fatalf("long waiter: %v", o.err)
		}
		if len(o.res.AutoAssign.Assigned) != 1 || len(o.res.Payouts.Cleared) != 1 {
			t.Errorf("long waiter result = %+v / %+v, want one booking and one cleared payout", o.res.AutoAssign, o.res.Payouts)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("long waiter never got the slot")
	}
	if n := f.bookedCharges(t, s); n != 1 || f.clearedDeposits(t, s) != 1 {
		t.Errorf("charge booked %d, deposit cleared %d; want 1 and 1", n, f.clearedDeposits(t, s))
	}
}

// reportShape is the Report API's three-fee-row shape for one order-linked
// charge and its payout (the 2000003 case): per-charge fee, settlement-level
// retained fee and the funds report's fees-retained.
func reportShape(fake *fakeVippsReport, date, msn, number, orderRef string, chargeOre, feeOre int64) {
	fake.funds[date] = []vipps.FundsEntry{
		{EntryType: "capture", Reference: orderRef, PspReference: "psp-" + number, Amount: chargeOre, LedgerDate: date, Time: date + "T08:00:00Z"},
		{EntryType: "fees-retained", PspReference: "900-" + date, Amount: -feeOre, LedgerDate: date},
		{EntryType: "payout-scheduled", Reference: "Utb. " + number + " Vippsnr " + msn, PspReference: "900-" + number, Amount: -(chargeOre - feeOre), LedgerDate: date},
	}
	fake.fees[date] = []vipps.FeeEntry{
		{EntryType: "fee", Reference: orderRef, PspReference: "psp-" + number, Amount: -feeOre, LedgerDate: date, Time: date + "T08:00:00Z"},
		{EntryType: "fees-retained", PspReference: "900-" + date, Amount: feeOre, LedgerDate: date},
	}
}

// A Report API sync by a person keeps its ingest when the booking slot stays
// busy and reports BookingDeferred; the next sync books what waited.
func TestSyncVippsReportDefersBookingWhileSlotHeld(t *testing.T) {
	f := newSlotFixture(t)
	ctx := context.Background()
	const date, msn = "2026-10-25", "123456"
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	order := f.club.NewOrderForInvoice(t, inv)
	fake := newFakeVippsReport(msn)
	reportShape(fake, date, msn, "7000001", order.VippsReference, 45000, 1446)
	f.svc.vippsBookingSlotWait = 100 * time.Millisecond
	release := f.holdSlot(t)

	res, err := f.svc.SyncVippsReport(ctx, fake, f.club.ID, f.club.Treasurer.ID, "", date)
	if err != nil {
		t.Fatalf("sync while the slot is held: %v", err)
	}
	if res.Imported != 5 || !res.BookingDeferred || res.Orders != nil || res.Payouts != nil {
		t.Fatalf("result = %+v, want 5 imported, booking deferred and nothing booked", res)
	}
	if n := f.entries(t); n != 0 {
		t.Errorf("%d entries booked while deferred", n)
	}

	release()
	again, err := f.svc.SyncVippsReport(ctx, fake, f.club.ID, f.club.Treasurer.ID, "", date)
	if err != nil {
		t.Fatalf("sync after release: %v", err)
	}
	if again.BookingDeferred || again.Orders == nil || again.Orders.Booked != 1 {
		t.Errorf("re-sync = %+v, want the order charge booked", again)
	}
}

// A deposit uploaded after its charges were booked clears on the upload, with
// the importing user, in the 2000003 shape: one 450 kr order-linked charge, a
// 14.46 fee in three rows and a 435.54 payout. Before the deposit exists the
// sweep reports the settlement as skipped (no bank deposit).
func TestBankUploadClearsVippsPayoutWhoseChargesAreBooked(t *testing.T) {
	f := newSlotFixture(t)
	ctx := context.Background()
	const date, msn, number = "2026-10-25", "123456", "7000001"
	booking := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	order := f.club.NewOrderForInvoice(t, inv)
	fake := newFakeVippsReport(msn)
	reportShape(fake, date, msn, number, order.VippsReference, 45000, 1446)
	if _, err := f.svc.SyncVippsReport(ctx, fake, f.club.ID, "", "", date); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	booked, err := f.svc.BookVippsQueue(ctx, f.club.ID, f.club.Treasurer.ID, 0)
	if err != nil {
		t.Fatalf("BookVippsQueue: %v", err)
	}
	if booked.Orders.Booked != 1 {
		t.Fatalf("orders = %+v, want the charge booked", booked.Orders)
	}
	if got := settlementsOf(booked.Payouts.Skipped)[number]; !strings.Contains(got, "ingen bankinnbetaling") {
		t.Fatalf("sweep before the upload: skipped = %+v, want the no-deposit reason", booked.Payouts.Skipped)
	}

	upload := []BankRow{{
		Date: booking.AddDate(0, 0, 2), Amount: 435.54, Reference: "SYNREF1",
		Description: "Utb. " + number + " Vippsnr " + msn,
	}}
	res, err := f.svc.ImportBankRows(ctx, f.club.ID, f.bankImp, "", upload)
	if err != nil {
		t.Fatalf("ImportBankRows: %v", err)
	}
	if res.Imported != 1 || res.BookingDeferred || res.VippsPayouts == nil || len(res.VippsPayouts.Cleared) != 1 {
		t.Fatalf("upload result = %+v / sweep %+v, want the deposit imported and the settlement cleared", res, res.VippsPayouts)
	}
	if res.VippsPayouts.Cleared[0].SettlementNumber != number {
		t.Errorf("cleared %+v, want settlement %s", res.VippsPayouts.Cleared, number)
	}

	var bankRow, bankEntry, feeEntry string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT id::text, journal_entry_id::text FROM bank_import_rows WHERE club_id = $1 AND description LIKE 'Utb. %'`, f.club.ID,
	).Scan(&bankRow, &bankEntry); err != nil {
		t.Fatalf("deposit not booked: %v", err)
	}
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT journal_entry_id::text FROM vipps_import_rows
		  WHERE club_id = $1 AND row_type = 'fee' AND journal_entry_id IS NOT NULL LIMIT 1`, f.club.ID,
	).Scan(&feeEntry); err != nil {
		t.Fatalf("fee not booked: %v", err)
	}
	if dr, _ := entryLineSum(t, f.svc, ctx, bankEntry, bankAccountCode); dr != 435.54 {
		t.Errorf("bank debit = %.2f, want 435.54", dr)
	}
	if dr, _ := entryLineSum(t, f.svc, ctx, feeEntry, vippsFeeAccountCode); dr != 14.46 {
		t.Errorf("fee expense = %.2f, want 14.46", dr)
	}
	balance, err := f.svc.settlementClearingBalance(ctx, f.club.ID, number, msn, booking, bankRow)
	if err != nil || !floatNear(balance, 0, 0.005) {
		t.Errorf("clearing balance = %.2f (err %v), want 0", balance, err)
	}

	// Uploading the file again changes nothing.
	again, err := f.svc.ImportBankRows(ctx, f.club.ID, f.bankImp, "", upload)
	if err != nil {
		t.Fatalf("second upload: %v", err)
	}
	if again.Imported != 0 || again.VippsPayouts == nil || len(again.VippsPayouts.Cleared) != 0 {
		t.Errorf("second upload = %+v / %+v, want no new clearing", again, again.VippsPayouts)
	}
}

// The upload's sweep leaves a settlement alone, with the reason, when it is
// not clearable: a charge still in Tildel (waiting), or a charge the payout
// counts that was never imported (the postings would not net to 0). Nothing
// is posted for either, and the deposit stays unbooked.
func TestBankUploadSweepSkipsSettlementWithMissingOrUnbookedCharge(t *testing.T) {
	f := newSlotFixture(t)
	ctx := context.Background()
	const msn = "123456"

	// 1: its charge is imported but no rule books it (no price fits 777 kr).
	f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 77700, Date: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), MSN: msn, CustomerName: "Synthetic Unknown"})
	f.club.NewVippsRow(t, fin.VippsRowOpts{RowType: "fee", Cents: -1000, Date: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), MSN: msn})
	f.club.NewVippsRow(t, fin.VippsRowOpts{RowType: "payout", Cents: -76700, Date: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), MSN: msn, Settlement: "8000001"})

	// 2: the payout counts two 1600 kr charges, only one was imported.
	d2 := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 160000, Date: d2, MSN: msn, CustomerName: "Synthetic Walk-in A"})
	f.club.NewVippsRow(t, fin.VippsRowOpts{RowType: "fee", Cents: -2000, Date: d2, MSN: msn})
	f.club.NewVippsRow(t, fin.VippsRowOpts{RowType: "payout", Cents: -318000, Date: d2, MSN: msn, Settlement: "8000002"})

	if _, err := f.svc.AutoAssignVippsCharges(ctx, f.club.ID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("auto-assign: %v", err)
	}
	before := f.entries(t)

	res, err := f.svc.ImportBankRows(ctx, f.club.ID, f.bankImp, "", []BankRow{
		{Date: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC), Amount: 767, Reference: "SYNREF2", Description: "Utb. 8000001 Vippsnr " + msn},
		{Date: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC), Amount: 3180, Reference: "SYNREF3", Description: "Utb. 8000002 Vippsnr " + msn},
	})
	if err != nil {
		t.Fatalf("ImportBankRows: %v", err)
	}
	sweep := res.VippsPayouts
	if sweep == nil || len(sweep.Cleared) != 0 || len(sweep.Failed) != 0 {
		t.Fatalf("sweep = %+v, want nothing cleared or failed", sweep)
	}
	if got := settlementsOf(sweep.Waiting)["8000001"]; !strings.Contains(got, "1 belastning") {
		t.Errorf("waiting = %+v, want 8000001 waiting on its charge", sweep.Waiting)
	}
	if got := settlementsOf(sweep.Skipped)["8000002"]; !strings.Contains(got, "går ikke i null") {
		t.Errorf("skipped = %+v, want 8000002 refused because it does not net to 0", sweep.Skipped)
	}
	if n := f.entries(t); n != before {
		t.Errorf("%d entries posted by the refused sweep", n-before)
	}
	if n := f.count(t, `SELECT count(*) FROM bank_import_rows WHERE club_id = $1 AND description LIKE 'Utb. %' AND journal_entry_id IS NOT NULL`, f.club.ID); n != 0 {
		t.Errorf("%d deposits booked, want none", n)
	}
}

// While the slot is held a bank upload keeps its import and reports the Vipps
// sweep as deferred, as does a bank sync for its pass 5; once the slot is free
// the next sync clears the settlement.
func TestBankUploadAndSyncDeferVippsSweepWhileSlotHeld(t *testing.T) {
	f := newSlotFixture(t)
	ctx := context.Background()
	s := f.settlement(t, "5000401", 2, 1, false)
	if _, err := f.svc.AutoAssignVippsCharges(ctx, f.club.ID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("auto-assign: %v", err)
	}
	f.exec(t, `DELETE FROM bank_import_rows WHERE id = $1`, s.bankRow)
	f.svc.vippsBookingSlotWait = 100 * time.Millisecond
	release := f.holdSlot(t)

	desc := "Utb. " + s.number + " Vippsnr " + sweepMSN
	row := BankRow{Date: s.booking.AddDate(0, 0, 1), Amount: 1550, Reference: "SYNREF4", Description: desc}
	up, err := f.svc.ImportBankRows(ctx, f.club.ID, f.bankImp, "", []BankRow{row})
	if err != nil {
		t.Fatalf("ImportBankRows: %v", err)
	}
	if up.Imported != 1 || !up.BookingDeferred || up.VippsPayouts != nil {
		t.Fatalf("upload = %+v / %+v, want the deposit imported and the sweep deferred", up, up.VippsPayouts)
	}
	sync, err := f.svc.BankSync(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("BankSync: %v", err)
	}
	if !sync.BookingDeferred || sync.VippsPayouts != nil {
		t.Errorf("bank sync = deferred %v / %+v, want pass 5 deferred", sync.BookingDeferred, sync.VippsPayouts)
	}
	if n := f.count(t, `SELECT count(*) FROM bank_import_rows WHERE club_id = $1 AND description = $2 AND journal_entry_id IS NOT NULL`, f.club.ID, desc); n != 0 {
		t.Fatalf("deposit booked while deferred")
	}

	release()
	sync, err = f.svc.BankSync(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("BankSync after release: %v", err)
	}
	if sync.BookingDeferred || sync.VippsPayouts == nil || len(sync.VippsPayouts.Cleared) != 1 {
		t.Errorf("bank sync after release = deferred %v / %+v, want the settlement cleared", sync.BookingDeferred, sync.VippsPayouts)
	}
}

// A concurrent winner is not stamped with the auto-assign rule or audited: a
// decision made before a person booked the charge in Tildel returns the
// person's entry as already reconciled, for both rules.
func TestBookVippsChargeDecisionLeavesAConcurrentWinnerAlone(t *testing.T) {
	for _, rule := range []string{autoAssignRuleMemberInvoice, autoAssignRulePriceItem} {
		t.Run(rule, func(t *testing.T) {
			f := newSlotFixture(t)
			ctx := context.Background()
			payer := f.club.NewMember(t, "member")
			f.exec(t, `UPDATE users SET first_name = 'Synthetic', last_name = 'Payerone' WHERE id = $1`, payer.ID)
			inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &payer, Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
			amount := int64(45000)
			if rule == autoAssignRulePriceItem {
				amount = 160000
			}
			row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: amount, CustomerName: "Synthetic Payerone"})

			cfg, err := f.svc.vippsAutoAssignConfig(ctx, f.club.ID)
			if err != nil {
				t.Fatalf("config: %v", err)
			}
			d, err := f.svc.decideVippsCharge(ctx, f.club.ID, cfg, "Synthetic Payerone", "", fin.Kr(amount), time.Date(f.club.Year, 6, 1, 0, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatalf("decide: %v", err)
			}
			if d.Rule != rule {
				t.Fatalf("decision = %+v, want the %s rule", d, rule)
			}

			// A person books the same charge before the pass acts on its decision.
			var winner string
			if rule == autoAssignRuleMemberInvoice {
				r, err := f.svc.AssignVippsChargeToInvoice(ctx, f.club.ID, row, inv.ID, f.club.Treasurer.ID)
				if err != nil {
					t.Fatalf("person's booking: %v", err)
				}
				winner = r.EntryID
			} else {
				r, err := f.svc.CreateVippsChargeBilag(ctx, f.club.ID, row, "3200", "", f.club.Treasurer.ID)
				if err != nil {
					t.Fatalf("person's booking: %v", err)
				}
				winner = r.EntryID
			}

			entry, already, err := f.svc.bookVippsChargeDecision(ctx, f.club.ID, f.club.Treasurer.ID, row, d)
			if err != nil || !already || entry != winner {
				t.Fatalf("bookVippsChargeDecision = %q, %v, %v; want the winner's entry %q and alreadyReconciled", entry, already, err, winner)
			}
			var ruleSet *string
			if err := f.club.Pool.QueryRow(ctx, `SELECT auto_assign_rule FROM vipps_import_rows WHERE id = $1`, row).Scan(&ruleSet); err != nil {
				t.Fatalf("read rule: %v", err)
			}
			if ruleSet != nil && *ruleSet != "" {
				t.Errorf("the person's booking was stamped with auto-assign rule %q", *ruleSet)
			}
			if n := f.autoAssignAudits(t); n != 0 {
				t.Errorf("%d auto_assigned audit rows for a booking the pass did not make", n)
			}

			// The pass's own bookkeeping treats it as neither booked nor waiting.
			item, _, err := f.svc.autoAssignOne(ctx, f.club.ID, f.club.Treasurer.ID, cfg, row, "Synthetic Payerone", "", fin.Kr(amount), time.Date(f.club.Year, 6, 1, 0, 0, 0, 0, time.UTC))
			if item != nil || err != nil && !errors.Is(err, errChargeBookedConcurrently) {
				t.Errorf("autoAssignOne on a booked charge = %+v, %v", item, err)
			}
		})
	}
}

// Correctness does not depend on the slot: the booking primitives called
// concurrently from two goroutines, bypassing BookVippsQueue and every slot, on
// a pool of two connections (so a pool call made under a row lock would hang)
// book each charge, faktura, settlement and bank deposit exactly once.
func TestBookingPrimitivesWithoutTheSlotBookOnce(t *testing.T) {
	f := newSlotFixture(t, testutil.WithMaxConns(2))
	a := f.settlement(t, "5000501", 2, 2, true)
	b := f.settlement(t, "5000502", 3, 2, false)
	// Enough charges in a third settlement that the racers overlap.
	c := f.settlement(t, "5000503", 4, 10, false)

	payer := f.club.NewMember(t, "member")
	f.exec(t, `UPDATE users SET first_name = 'Synthetic', last_name = 'Bankpayer' WHERE id = $1`, payer.ID)
	f.club.NewInvoice(t, fin.InvoiceOpts{Member: &payer, Cents: 30000, Lines: []fin.Line{{Category: "membership", Cents: 30000}}})
	deposit := f.club.NewBankRow(t, f.bankImp, fin.BankRowOpts{
		Cents: 30000, Counterpart: "Synthetic Bankpayer", FromAccount: "12345600001",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var (
		wg      sync.WaitGroup
		start   = make(chan struct{})
		mu      sync.Mutex
		orders  int
		assign  int
		review  int
		cleared int
		bank    int
		errs    []error
	)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			o, err := f.svc.AutoReconcileOrderCharges(ctx, f.club.ID, f.club.Treasurer.ID)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("orders: %w", err))
				mu.Unlock()
				return
			}
			aa, err := f.svc.AutoAssignVippsCharges(ctx, f.club.ID, f.club.Treasurer.ID)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("auto-assign: %w", err))
				mu.Unlock()
				return
			}
			p, err := f.svc.ClearReadyVippsPayouts(ctx, f.club.ID, f.club.Treasurer.ID)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("sweep: %w", err))
				mu.Unlock()
				return
			}
			bk, err := f.svc.AutoAssignBankDeposits(ctx, f.club.ID, f.club.Treasurer.ID, BankAutoAssignOpts{Trigger: BankAutoAssignTriggerSync})
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("bank: %w", err))
				mu.Unlock()
				return
			}
			mu.Lock()
			orders += o.Booked
			assign += len(aa.Assigned)
			review += aa.NeedsReview
			cleared += len(p.Cleared)
			bank += len(bk.Assigned)
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()
	if ctx.Err() != nil {
		t.Fatal("the primitives hung on a pool of two connections")
	}
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}

	// The goroutines split the work, but together book it once. The ledger
	// is the judge of the settlements: a loser's sweep may report the one a
	// winner cleared as failed.
	if orders != 1 || assign != 14 || bank != 1 {
		t.Errorf("booked %d order charges, %d auto-assigned, %d bank deposits; want 1, 14 and 1", orders, assign, bank)
	}
	// A charge a concurrent pass won is neither this pass's booking nor a
	// charge waiting for a person.
	if review != 0 {
		t.Errorf("auto-assign passes reported %d charges as needing review, want 0", review)
	}
	if cleared != 3 {
		t.Errorf("cleared %d settlements, want 3", cleared)
	}
	if n := f.bookedCharges(t, a, b, c); n != 15 {
		t.Errorf("%d of 15 charges booked", n)
	}
	if n := f.clearedDeposits(t, a, b, c); n != 3 {
		t.Errorf("%d of 3 settlement deposits cleared", n)
	}
	// 15 charges, 3 settlements of fee + bank, 1 faktura deposit.
	if n := f.entries(t); n != 22 {
		t.Errorf("%d journal entries, want 22", n)
	}
	if n := f.autoAssignAudits(t); n != 14 {
		t.Errorf("%d auto_assigned audit rows, want 14", n)
	}
	if f.count(t, `SELECT count(*) FROM bank_import_rows WHERE id = $1 AND journal_entry_id IS NOT NULL`, deposit) != 1 {
		t.Errorf("the faktura deposit was not booked once")
	}
	if n := f.count(t, `SELECT count(*) FROM payments WHERE club_id = $1`, f.club.ID); n != 2 {
		t.Errorf("%d payments, want 2 (the order's faktura and the bank-paid one)", n)
	}
}

// A failing step does not stop the later ones: with the order step failing
// (its repair query cannot run), the auto-assign rules still book and the sweep
// still clears, and the error names the failing step.
func TestBookVippsQueueSweepsAfterAFailingStep(t *testing.T) {
	f := newSlotFixture(t)
	s := f.settlement(t, "5000701", 2, 1, false)
	f.exec(t, `ALTER TABLE vipps_imports RENAME COLUMN filename TO filename_unavailable`)

	res, err := f.svc.BookVippsQueue(context.Background(), f.club.ID, f.club.Treasurer.ID, 0)
	if err == nil || !strings.Contains(err.Error(), "booking order-linked charges") {
		t.Fatalf("error = %v, want the order step named", err)
	}
	if res == nil || res.AutoAssign == nil || len(res.AutoAssign.Assigned) != 1 {
		t.Fatalf("result = %+v, want the auto-assign step to have booked its charge", res)
	}
	if res.Payouts == nil || len(res.Payouts.Cleared) != 1 {
		t.Fatalf("sweep = %+v, want the settlement cleared although the order step failed", res.Payouts)
	}
	if n := f.clearedDeposits(t, s); n != 1 {
		t.Errorf("%d deposits cleared, want 1", n)
	}
}

// A request cancelled while an item is in flight stops there: the pass returns
// the context's error with what it did, instead of turning every remaining
// item into a skipped one.
func TestBookingPassesStopOnACancelledRequest(t *testing.T) {
	t.Run("order-linked charges", func(t *testing.T) {
		f := newSlotFixture(t)
		var invs []fin.Invoice
		for range 3 {
			inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
			order := f.club.NewOrderForInvoice(t, inv)
			// Processed oldest first (tx_at), so the second charge is the one blocked below.
			row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, OrderID: order.VippsReference, Date: time.Date(2026, 6, 1+len(invs), 0, 0, 0, 0, time.UTC)})
			f.exec(t, `UPDATE vipps_import_rows SET tx_at = booking_date + interval '8 hours' WHERE id = $1`, row)
			invs = append(invs, inv)
		}
		lock := testutil.HoldRowLock(t, f.club.Pool, `SELECT id FROM invoices WHERE id = $1 FOR UPDATE`, invs[1].ID)
		res, err := runCancelledWhileBlocked(t, lock, func(ctx context.Context) (*VippsOrderReconcileResult, error) {
			return f.svc.AutoReconcileOrderCharges(ctx, f.club.ID, f.club.Treasurer.ID)
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		// The first charge was booked; the one in flight is reported; the
		// third was never tried.
		if res.Booked != 1 || len(res.SkippedDetails) != 1 {
			t.Errorf("result = %+v, want 1 booked and only the in-flight charge skipped", res)
		}
	})

	t.Run("auto-assign", func(t *testing.T) {
		f := newSlotFixture(t)
		var invs []fin.Invoice
		for i := range 3 {
			m := f.club.NewMember(t, "member")
			f.exec(t, `UPDATE users SET first_name = 'Synthetic', last_name = $2 WHERE id = $1`, m.ID, fmt.Sprintf("Payer%c", 'a'+i))
			invs = append(invs, f.club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}}))
			// Processed oldest first (tx_at), so the second charge is the one blocked below.
			row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: fmt.Sprintf("Synthetic Payer%c", 'a'+i), Date: time.Date(2026, 6, 1+i, 0, 0, 0, 0, time.UTC)})
			f.exec(t, `UPDATE vipps_import_rows SET tx_at = booking_date + interval '8 hours' WHERE id = $1`, row)
		}
		lock := testutil.HoldRowLock(t, f.club.Pool, `SELECT id FROM invoices WHERE id = $1 FOR UPDATE`, invs[1].ID)
		res, err := runCancelledWhileBlocked(t, lock, func(ctx context.Context) (*AutoAssignResult, error) {
			return f.svc.AutoAssignVippsCharges(ctx, f.club.ID, f.club.Treasurer.ID)
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if len(res.Assigned) != 1 || res.NeedsReview != 1 {
			t.Errorf("result = assigned %d, needs review %d; want 1 booked and only the in-flight charge left", len(res.Assigned), res.NeedsReview)
		}
	})

	t.Run("payout sweep", func(t *testing.T) {
		f := newSlotFixture(t)
		var settlements []slotSettlement
		for i, day := range []int{2, 3, 4} {
			s := f.settlement(t, fmt.Sprintf("50006%02d", i), day, 1, false)
			settlements = append(settlements, s)
		}
		if _, err := f.svc.AutoAssignVippsCharges(context.Background(), f.club.ID, f.club.Treasurer.ID); err != nil {
			t.Fatalf("auto-assign: %v", err)
		}
		lock := testutil.HoldRowLock(t, f.club.Pool,
			`SELECT id FROM vipps_import_rows WHERE club_id = $1 AND row_type = 'payout' AND settlement_number = $2 FOR UPDATE`,
			f.club.ID, settlements[1].number)
		res, err := runCancelledWhileBlocked(t, lock, func(ctx context.Context) (*VippsPayoutSweepResult, error) {
			return f.svc.ClearReadyVippsPayouts(ctx, f.club.ID, f.club.Treasurer.ID)
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if len(res.Cleared) != 1 || len(res.Failed) != 1 {
			t.Errorf("result = cleared %d, failed %d; want the first cleared and only the in-flight one failed", len(res.Cleared), len(res.Failed))
		}
		if n := f.clearedDeposits(t, settlements...); n != 1 {
			t.Errorf("%d deposits cleared, want 1", n)
		}
	})
}

// runCancelledWhileBlocked runs fn until it blocks on lock, cancels its
// context and lets the lock go, and returns what fn returned.
func runCancelledWhileBlocked[T any](t *testing.T, lock *testutil.RowLock, fn func(ctx context.Context) (T, error)) (T, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type out struct {
		res T
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := fn(ctx)
		done <- out{res, err}
	}()
	lock.WaitForBlocked(30 * time.Second)
	cancel()
	select {
	case o := <-done:
		lock.Release()
		return o.res, o.err
	case <-time.After(30 * time.Second):
		lock.Release()
		t.Fatal("the cancelled pass did not return")
		panic("unreachable")
	}
}
