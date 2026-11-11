package accounting

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

// Tests of the automatic booking run (BRY-567, ACC-13): the switch, the
// ingest, the bank and Vipps passes as the system actor, the summary audit
// row, the observer, and what the run never does (guess by name, book a
// declined row, open a year).

// fakeAutoObserver records what the run tells the BRY-571 metrics.
type fakeAutoObserver struct {
	mu     sync.Mutex
	synced []string
	runs   []bool
}

func (o *fakeAutoObserver) VippsReportSynced(msn string, _ time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.synced = append(o.synced, msn)
}

func (o *fakeAutoObserver) AutoBookingRun(provider string, failed bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if provider != AutoBookingProviderVipps {
		panic("unexpected provider " + provider)
	}
	o.runs = append(o.runs, failed)
}

// autoOn turns the club's automatic booking switch on.
func (f *slotFixture) autoOn(t *testing.T) {
	t.Helper()
	f.exec(t, `UPDATE clubs SET automatic_booking = true WHERE id = $1`, f.club.ID)
}

// clockAfter makes the run's Report API pull ask for date as d-1.
func (f *slotFixture) clockAfter(t *testing.T, date string) {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatal(err)
	}
	f.svc.autoBookingNow = func() time.Time { return d.AddDate(0, 0, 1).Add(12 * time.Hour) }
}

func (f *slotFixture) runAuto(t *testing.T, src VippsReportSource) *AutoBookingResult {
	t.Helper()
	return f.svc.RunAutomaticBooking(context.Background(), f.club.ID, AutoBookingOpts{
		Source: src, Trigger: AutoBookingTriggerNightly, Accounting: true,
	})
}

// autoRun is one accounting.auto_booking_run audit row.
type autoRun struct {
	actor   *string
	details map[string]any
}

func (f *slotFixture) autoRuns(t *testing.T) []autoRun {
	t.Helper()
	rows, err := f.club.Pool.Query(context.Background(),
		`SELECT actor_id::text, details FROM audit_log WHERE club_id = $1 AND action = $2 ORDER BY created_at, id`,
		f.club.ID, audit.ActionAccountingAutoBookingRun)
	if err != nil {
		t.Fatalf("audit rows: %v", err)
	}
	defer rows.Close()
	var out []autoRun
	for rows.Next() {
		var r autoRun
		var raw []byte
		if err := rows.Scan(&r.actor, &raw); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		if err := json.Unmarshal(raw, &r.details); err != nil {
			t.Fatalf("audit details: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func (f *slotFixture) systemActor(t *testing.T) string {
	t.Helper()
	id, err := f.svc.SystemActor(context.Background(), f.club.ID)
	if err != nil {
		t.Fatalf("SystemActor: %v", err)
	}
	return id
}

func (f *slotFixture) bankRowEntry(t *testing.T, rowID string) string {
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

func (f *slotFixture) invoicePaid(t *testing.T, invoiceID string) bool {
	t.Helper()
	return f.count(t, `SELECT count(*) FROM invoices WHERE id = $1 AND payment_id IS NOT NULL`, invoiceID) == 1
}

// kidRow seeds an open faktura and an incoming bank row paying it by KID.
func (f *slotFixture) kidRow(t *testing.T, date time.Time, cents int64) (row string, inv fin.Invoice) {
	t.Helper()
	inv = f.club.NewInvoice(t, fin.InvoiceOpts{Cents: cents, IssueDate: date.AddDate(0, -1, 0)})
	row = f.club.NewBankRow(t, f.bankImp, fin.BankRowOpts{Date: date, Cents: cents, KID: inv.KID, Description: "Synthetic KID payment"})
	return row, inv
}

// namedDeposit seeds a member "Synthetic <last>", an open faktura for them
// and a KID-less deposit from them for the same amount.
func (f *slotFixture) namedDeposit(t *testing.T, last string, cents int64) (row string, inv fin.Invoice) {
	t.Helper()
	m := f.club.NewMember(t, "member")
	f.exec(t, `UPDATE users SET first_name = 'Synthetic', last_name = $2 WHERE id = $1`, m.ID, last)
	inv = f.club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Lines: []fin.Line{{Category: "membership", Cents: cents}}})
	row = f.club.NewBankRow(t, f.bankImp, fin.BankRowOpts{
		Cents: cents, Counterpart: "Synthetic " + last, FromAccount: "12345600" + testutil.RandomHex(2),
		Description: "Synthetic deposit",
	})
	return row, inv
}

// transferPair seeds the two rows of one transfer between 1920 and 1925.
func (f *slotFixture) transferPair(t *testing.T, cents int64, day int) (out, in string) {
	t.Helper()
	date := time.Date(f.club.Year, 7, day, 0, 0, 0, 0, time.UTC)
	out = f.club.NewBankRow(t, f.club.NewBankImportOn(t, "1920"), fin.BankRowOpts{Date: date, Cents: -cents, Description: "Synthetic transfer out"})
	in = f.club.NewBankRow(t, f.club.NewBankImportOn(t, "1925"), fin.BankRowOpts{Date: date, Cents: cents, Description: "Synthetic transfer in"})
	return out, in
}

func auditNum(m map[string]any, key string) float64 {
	v, _ := m[key].(float64)
	return v
}

func auditSub(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

// Regression only: with the switch off the run ingests and books nothing,
// writes no summary row, and still tells the observer the pull succeeded.
func TestRunAutomaticBookingSwitchOffIngestsOnly(t *testing.T) {
	f := newSlotFixture(t)
	const date, msn = "2026-06-10", "123456"
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	order := f.club.NewOrderForInvoice(t, inv)
	fake := newFakeVippsReport(msn)
	reportShape(fake, date, msn, "2000101", order.VippsReference, 45000, 1446)
	f.clockAfter(t, date)
	obs := &fakeAutoObserver{}
	f.svc.SetAutoBookingObserver(obs)

	res := f.runAuto(t, fake)
	if res.Booked || res.Ingest == nil || !res.Ingest.OK || res.Ingest.Imported != 5 {
		t.Fatalf("result = %+v / ingest %+v, want 5 rows ingested and nothing booked", res, res.Ingest)
	}
	if n := f.entries(t); n != 0 {
		t.Errorf("%d entries with the switch off", n)
	}
	if runs := f.autoRuns(t); len(runs) != 0 {
		t.Errorf("%d summary rows with the switch off", len(runs))
	}
	if !slices.Equal(obs.synced, []string{msn}) || !slices.Equal(obs.runs, []bool{false}) {
		t.Errorf("observer synced %v runs %v, want one sync and one non-failed run", obs.synced, obs.runs)
	}
}

// With the switch on the run books the order-linked charge (faktura paid),
// the unambiguous price-item charge (with its rule and trigger), leaves the
// ambiguous one, clears the settlement (nets to 0), all as the system actor,
// and writes one summary row.
func TestRunAutomaticBookingBooksAndClears(t *testing.T) {
	f := newSlotFixture(t)
	ctx := context.Background()
	f.autoOn(t)
	s := f.settlement(t, "5000201", 2, 1, true)
	ambiguous := f.club.NewVippsRow(t, fin.VippsRowOpts{
		Cents: 77700, Date: time.Date(f.club.Year, 6, 20, 0, 0, 0, 0, time.UTC), MSN: sweepMSN, CustomerName: "Synthetic Nobody",
	})

	res := f.runAuto(t, nil)
	if !res.Booked || res.Failed() || res.Busy || res.Aborted || res.Ingest != nil {
		t.Fatalf("result = %+v, want a clean booked run without ingest", res)
	}
	if res.Vipps == nil || res.Vipps.Orders.Booked != 1 || len(res.Vipps.AutoAssign.Assigned) != 1 || len(res.Vipps.Payouts.Cleared) != 1 {
		t.Fatalf("vipps = %+v / %+v / %+v, want one order, one auto-assigned, one cleared", res.Vipps.Orders, res.Vipps.AutoAssign, res.Vipps.Payouts)
	}
	if f.bookedCharges(t, s) != 2 || f.clearedDeposits(t, s) != 1 {
		t.Fatalf("booked %d charges, cleared %d deposits; want 2 and 1", f.bookedCharges(t, s), f.clearedDeposits(t, s))
	}
	if !f.invoicePaid(t, s.order.ID) {
		t.Error("the order's faktura is not paid")
	}
	if rowEntry(t, f.club, ambiguous) != "" {
		t.Error("the ambiguous charge was booked")
	}
	priceCharge := s.charges[0]
	var rule, trigger string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT r.auto_assign_rule, a.details->>'trigger' FROM vipps_import_rows r
		   JOIN audit_log a ON a.resource_id = r.id::text AND a.action = $2
		  WHERE r.id = $1`, priceCharge, audit.ActionVippsChargeAutoAssigned).Scan(&rule, &trigger); err != nil {
		t.Fatalf("auto-assign marker: %v", err)
	}
	if rule != autoAssignRulePriceItem || trigger != AutoBookingTriggerNightly {
		t.Errorf("rule %q trigger %q, want price_item / nightly", rule, trigger)
	}
	bal, err := f.svc.settlementClearingBalance(ctx, f.club.ID, s.number, sweepMSN, s.booking, s.bankRow)
	if err != nil || !floatNear(bal, 0, 0.005) {
		t.Errorf("clearing balance = %.2f (err %v), want 0", bal, err)
	}

	system := f.systemActor(t)
	if n := f.count(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1 AND created_by <> $2`, f.club.ID, system); n != 0 {
		t.Errorf("%d entries not created by the system actor", n)
	}
	je, err := f.svc.GetJournalEntry(ctx, f.club.ID, f.bankRowEntry(t, s.bankRow))
	if err != nil || !je.Automatic {
		t.Errorf("deposit entry = %+v (err %v), want Automatic", je, err)
	}

	runs := f.autoRuns(t)
	if len(runs) != 1 {
		t.Fatalf("%d summary rows, want 1", len(runs))
	}
	r := runs[0]
	if r.actor == nil || *r.actor != system {
		t.Errorf("summary actor = %v, want the system actor", r.actor)
	}
	v := auditSub(r.details, "vipps")
	if r.details["provider"] != "vipps" || r.details["trigger"] != AutoBookingTriggerNightly ||
		auditNum(v, "orders_booked") != 1 || auditNum(v, "auto_assigned") != 1 || auditNum(v, "needs_review") != 1 {
		t.Errorf("summary details = %v", r.details)
	}
	if cleared, _ := v["payouts_cleared"].([]any); len(cleared) != 1 || cleared[0] != s.number {
		t.Errorf("payouts_cleared = %v, want [%s]", v["payouts_cleared"], s.number)
	}
}

// A run with no Report API source books the rows a CSV upload brought.
func TestRunAutomaticBookingWithoutSourceBooksCSVRows(t *testing.T) {
	f := newSlotFixture(t)
	f.autoOn(t)
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	order := f.club.NewOrderForInvoice(t, inv)
	charge := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, MSN: sweepMSN, OrderID: order.VippsReference})

	res := f.runAuto(t, nil)
	if res.Ingest != nil || res.IngestFailed || res.Vipps == nil || res.Vipps.Orders.Booked != 1 {
		t.Fatalf("result = %+v, want no ingest and the CSV charge booked", res)
	}
	if rowEntry(t, f.club, charge) == "" || !f.invoicePaid(t, inv.ID) {
		t.Error("the CSV charge is not booked or its faktura not paid")
	}
}

// A failed Report API pull does not stop the booking of what is already in
// Brygge; the summary says ingest_failed and the observer hears no success.
func TestRunAutomaticBookingIngestFailsStillBooks(t *testing.T) {
	f := newSlotFixture(t)
	f.autoOn(t)
	s := f.settlement(t, "5000301", 3, 1, false)
	fake := newFakeVippsReport("123456")
	fake.fundsErr = errors.New("synthetic outage")
	f.clockAfter(t, "2026-06-10")
	obs := &fakeAutoObserver{}
	f.svc.SetAutoBookingObserver(obs)

	res := f.runAuto(t, fake)
	if !res.IngestFailed || res.Ingest.OK || len(res.Ingest.FailedDates) != vippsReportDays {
		t.Fatalf("ingest = %+v, want every date failed", res.Ingest)
	}
	if f.bookedCharges(t, s) != 1 {
		t.Error("the run did not book the rows already present")
	}
	if len(obs.synced) != 0 || !slices.Equal(obs.runs, []bool{false}) {
		t.Errorf("observer synced %v runs %v; want no sync success and one run that is not failed", obs.synced, obs.runs)
	}
	runs := f.autoRuns(t)
	if len(runs) != 1 || runs[0].details["ingest_failed"] != true {
		t.Fatalf("summary rows = %+v, want one with ingest_failed", runs)
	}
	if dates, _ := auditSub(runs[0].details, "ingest")["failed_dates"].([]any); len(dates) != vippsReportDays {
		t.Errorf("failed_dates = %v", auditSub(runs[0].details, "ingest"))
	}
}

// A report not finalised yet is an answer: the pull counts as a success.
func TestRunAutomaticBookingTryLaterIsSuccess(t *testing.T) {
	f := newSlotFixture(t)
	fake := newFakeVippsReport("123456")
	fake.tryLaterFunds = true
	f.clockAfter(t, "2026-06-10")
	obs := &fakeAutoObserver{}
	f.svc.SetAutoBookingObserver(obs)

	res := f.runAuto(t, fake)
	if res.IngestFailed || !res.Ingest.OK || len(res.Ingest.TryLaterDates) != vippsReportDays {
		t.Fatalf("ingest = %+v, want every date try-later and the pull OK", res.Ingest)
	}
	if !slices.Equal(obs.synced, []string{"123456"}) {
		t.Errorf("observer synced %v, want the merchant", obs.synced)
	}
}

// A club on the legacy Vipps model is never booked unattended on the Vipps
// side; its bank auto-assign still books.
func TestRunAutomaticBookingLegacyClubBooksBankOnly(t *testing.T) {
	f := newSlotFixture(t)
	f.autoOn(t)
	f.exec(t, `UPDATE clubs SET vipps_per_charge_enabled = false WHERE id = $1`, f.club.ID)
	deposit, inv := f.namedDeposit(t, "Legacyclub", 52000)
	orderInv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	order := f.club.NewOrderForInvoice(t, orderInv)
	charge := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, MSN: sweepMSN, OrderID: order.VippsReference})

	res := f.runAuto(t, nil)
	if !res.VippsSkippedLegacy || res.Vipps != nil {
		t.Fatalf("result = %+v, want the Vipps step skipped as legacy", res)
	}
	if res.BankAutoAssign == nil || len(res.BankAutoAssign.Assigned) != 1 || f.bankRowEntry(t, deposit) == "" || !f.invoicePaid(t, inv.ID) {
		t.Fatalf("bank auto-assign = %+v, want the deposit booked", res.BankAutoAssign)
	}
	if rowEntry(t, f.club, charge) != "" {
		t.Error("a legacy club's Vipps charge was booked unattended")
	}
	var trigger string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT details->>'trigger' FROM audit_log WHERE action = $1 AND resource_id = $2`,
		audit.ActionBankRowAutoAssigned, deposit).Scan(&trigger); err != nil || trigger != AutoBookingTriggerNightly {
		t.Errorf("bank_row.auto_assigned trigger = %q (err %v), want nightly", trigger, err)
	}
	runs := f.autoRuns(t)
	if len(runs) != 1 || runs[0].details["vipps_skipped_legacy"] != true || auditNum(auditSub(runs[0].details, "bank"), "auto_assigned") != 1 {
		t.Errorf("summary rows = %+v", runs)
	}
}

// The 2000003 shape through the whole run: ingested from the Report API,
// the order charge booked, the fee 14.46, the bank 435.54, netting to 0.
// Without the deposit the settlement is skipped with the no-deposit reason.
func TestRunAutomaticBooking2000003Shape(t *testing.T) {
	const date, msn, number = "2026-06-10", "123456", "2000003"
	booking := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	setup := func(t *testing.T) (*slotFixture, *fakeVippsReport) {
		f := newSlotFixture(t)
		f.autoOn(t)
		inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
		order := f.club.NewOrderForInvoice(t, inv)
		fake := newFakeVippsReport(msn)
		reportShape(fake, date, msn, number, order.VippsReference, 45000, 1446)
		f.clockAfter(t, date)
		return f, fake
	}

	t.Run("with the deposit", func(t *testing.T) {
		f, fake := setup(t)
		ctx := context.Background()
		bankRow := f.club.NewBankRow(t, f.bankImp, fin.BankRowOpts{
			Date: booking.AddDate(0, 0, 1), Cents: 43554, Description: "Utb. " + number + " Vippsnr " + msn,
		})
		res := f.runAuto(t, fake)
		if res.Vipps == nil || len(res.Vipps.Payouts.Cleared) != 1 {
			t.Fatalf("result = %+v, want the settlement cleared", res.Vipps)
		}
		var feeEntry string
		if err := f.club.Pool.QueryRow(ctx,
			`SELECT journal_entry_id::text FROM vipps_import_rows
			  WHERE club_id = $1 AND row_type = 'fee' AND journal_entry_id IS NOT NULL LIMIT 1`, f.club.ID,
		).Scan(&feeEntry); err != nil {
			t.Fatalf("fee not booked: %v", err)
		}
		if dr, _ := entryLineSum(t, f.svc, ctx, f.bankRowEntry(t, bankRow), bankAccountCode); dr != 435.54 {
			t.Errorf("bank debit = %.2f, want 435.54", dr)
		}
		if dr, _ := entryLineSum(t, f.svc, ctx, feeEntry, vippsFeeAccountCode); dr != 14.46 {
			t.Errorf("fee expense = %.2f, want 14.46", dr)
		}
		bal, err := f.svc.settlementClearingBalance(ctx, f.club.ID, number, msn, booking, bankRow)
		if err != nil || !floatNear(bal, 0, 0.005) {
			t.Errorf("clearing balance = %.2f (err %v), want 0", bal, err)
		}
	})

	t.Run("without the deposit", func(t *testing.T) {
		f, fake := setup(t)
		res := f.runAuto(t, fake)
		if res.Vipps == nil || res.Vipps.Orders.Booked != 1 {
			t.Fatalf("result = %+v, want the charge booked", res.Vipps)
		}
		if got := settlementsOf(res.Vipps.Payouts.Skipped)[number]; !strings.Contains(got, "ingen bankinnbetaling") {
			t.Errorf("skipped = %+v, want the no-deposit reason", res.Vipps.Payouts.Skipped)
		}
	})
}

// A settlement whose postings do not net to 0 (BRY-560) is refused with
// nothing posted; the summary lists it with the failed payouts and its reason.
func TestRunAutomaticBookingNotNettingIsReported(t *testing.T) {
	f := newSlotFixture(t)
	ctx := context.Background()
	f.autoOn(t)
	pf := &payoutSweepFixture{svc: f.svc, club: f.club, bankImport: f.bankImp}
	s := pf.feeSettlement(t, "5000501", []int64{10000}, []feeRowSpec{{amount: -500}}, 14500)
	stray := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 5000, Date: s.booking, MSN: sweepMSN})
	f.exec(t, `UPDATE vipps_import_rows SET dismissed_at = now() WHERE id = $1`, stray)
	before := pf.postedEntries(t)

	res := f.runAuto(t, nil)
	if after := pf.postedEntries(t); after != before {
		t.Errorf("the run posted %d entries for a settlement that does not net", after-before)
	}
	if f.bankRowEntry(t, s.bankRow) != "" {
		t.Error("the deposit was linked")
	}
	if res.Failed() || !res.NeedsAudit() {
		t.Errorf("Failed %v NeedsAudit %v; want a summary row but not a failed run", res.Failed(), res.NeedsAudit())
	}
	runs := f.autoRuns(t)
	if len(runs) != 1 {
		t.Fatalf("%d summary rows, want 1", len(runs))
	}
	failed, _ := auditSub(runs[0].details, "vipps")["payouts_failed"].([]any)
	if len(failed) != 1 {
		t.Fatalf("payouts_failed = %v, want the settlement", failed)
	}
	item, _ := failed[0].(map[string]any)
	if item["settlement"] != s.number || !strings.Contains(item["reason"].(string), "går ikke i null") {
		t.Errorf("payouts_failed[0] = %v, want BRY-560's reason", item)
	}
	_ = ctx
}

// Reference years are never touched; a closed year is reported and the run
// goes on.
func TestRunAutomaticBookingReferenceAndClosedPeriods(t *testing.T) {
	f := newSlotFixture(t)
	f.autoOn(t)
	wf := &waitFixture{svc: f.svc, club: f.club, imp: f.bankImp}
	wf.period(t, 2025, "open", true)
	wf.period(t, 2024, "closed", false)
	refKID, refInv := f.kidRow(t, time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC), 30000)
	refCharge := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 160000, Date: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC), MSN: sweepMSN, CustomerName: "Synthetic Refyear"})
	closedKID, _ := f.kidRow(t, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), 31000)
	openKID, openInv := f.kidRow(t, time.Date(f.club.Year, 6, 1, 0, 0, 0, 0, time.UTC), 32000)

	res := f.runAuto(t, nil)
	if f.bankRowEntry(t, refKID) != "" || f.invoicePaid(t, refInv.ID) || rowEntry(t, f.club, refCharge) != "" {
		t.Error("a reference-year row was booked")
	}
	if f.bankRowEntry(t, closedKID) != "" {
		t.Error("a row in a closed year was booked")
	}
	if !slices.Equal(res.ClosedPeriods, []string{"2024"}) || len(res.MissingPeriods) != 0 {
		t.Errorf("closed %v missing %v, want [2024] and none", res.ClosedPeriods, res.MissingPeriods)
	}
	if res.KIDMatched != 1 || f.bankRowEntry(t, openKID) == "" || !f.invoicePaid(t, openInv.ID) {
		t.Errorf("KID matched %d; want the open year's row booked after the closed one", res.KIDMatched)
	}
	runs := f.autoRuns(t)
	if len(runs) != 1 {
		t.Fatalf("%d summary rows, want 1", len(runs))
	}
	if closed, _ := runs[0].details["closed_periods"].([]any); len(closed) != 1 || closed[0] != "2024" {
		t.Errorf("closed_periods = %v", runs[0].details["closed_periods"])
	}
}

// A KID row dated in a year that does not exist waits; once the treasurer
// creates the year, the next run books it there, on its own date.
func TestRunAutomaticBookingKIDRowBooksOnceYearExists(t *testing.T) {
	f := newSlotFixture(t)
	ctx := context.Background()
	f.autoOn(t)
	date := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	row, inv := f.kidRow(t, date, 30000)

	res := f.runAuto(t, nil)
	if f.bankRowEntry(t, row) != "" || !slices.Equal(res.MissingPeriods, []string{"2025"}) {
		t.Fatalf("first run: entry %q missing %v; want the row waiting for 2025", f.bankRowEntry(t, row), res.MissingPeriods)
	}
	if n := f.count(t, `SELECT count(*) FROM fiscal_periods WHERE club_id = $1 AND year = 2025`, f.club.ID); n != 0 {
		t.Fatal("the run opened 2025")
	}

	wf := &waitFixture{svc: f.svc, club: f.club, imp: f.bankImp}
	period := wf.period(t, 2025, "open", false)
	res = f.runAuto(t, nil)
	entry := f.bankRowEntry(t, row)
	if res.KIDMatched != 1 || entry == "" || !f.invoicePaid(t, inv.ID) {
		t.Fatalf("second run matched %d, entry %q; want the row booked", res.KIDMatched, entry)
	}
	je, err := f.svc.GetJournalEntry(ctx, f.club.ID, entry)
	if err != nil || je.FiscalPeriodID != period || je.EntryDate != "2025-06-01" || !je.Automatic {
		t.Errorf("entry = %+v (err %v), want 2025-06-01 in the 2025 period, automatic", je, err)
	}
}

// The counterpart-name + amount guess is never used by the run, even with
// bank auto-assign off; a person's Synkroniser still books it.
func TestRunAutomaticBookingNeverUsesNameMatch(t *testing.T) {
	f := newSlotFixture(t)
	f.autoOn(t)
	f.exec(t, `UPDATE clubs SET bank_auto_assign = false WHERE id = $1`, f.club.ID)
	row, inv := f.namedDeposit(t, "Namematch", 33300)

	res := f.runAuto(t, nil)
	if res.KIDMatched != 0 || res.BankAutoAssign != nil || f.bankRowEntry(t, row) != "" {
		t.Fatalf("run = %+v; want the name-only row left", res)
	}
	sync, err := f.svc.BankSync(context.Background(), f.club.ID, f.club.Treasurer.ID)
	if err != nil || sync.KIDMatched != 1 || !f.invoicePaid(t, inv.ID) {
		t.Fatalf("person's sync = %+v (err %v), want the name match booked", sync, err)
	}
}

// A person's undo is final for the run (ACC-13(b)): a charge, an order
// charge, a KID row, an auto-assigned deposit, a transfer and a cleared
// payout deposit ("Angre innbetalingen"), each booked by the run and undone
// by a person, stay unbooked through the next run. This is the
// vipps_sticky_undo matrix run through RunAutomaticBooking.
func TestRunAutomaticBookingHonoursDeclines(t *testing.T) {
	f := newSlotFixture(t)
	ctx := context.Background()
	f.autoOn(t)
	charge := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 160000, MSN: sweepMSN, CustomerName: "Synthetic Walk-in"})
	orderInv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	order := f.club.NewOrderForInvoice(t, orderInv)
	orderCharge := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, MSN: sweepMSN, OrderID: order.VippsReference})
	kid, _ := f.kidRow(t, time.Date(f.club.Year, 6, 1, 0, 0, 0, 0, time.UTC), 30000)
	deposit, _ := f.namedDeposit(t, "Sticky", 52000)
	out, in := f.transferPair(t, 100000, 3)
	pf := &payoutSweepFixture{svc: f.svc, club: f.club, bankImport: f.bankImp}
	payout := pf.settlement(t, "5001001", 12, []int64{120000}, 5000, true)
	pf.book(t, payout.charges...)

	res := f.runAuto(t, nil)
	if res.KIDMatched != 1 || res.TransfersLinked != 1 || len(res.BankAutoAssign.Assigned) != 1 ||
		res.Vipps.Orders.Booked != 1 || len(res.Vipps.AutoAssign.Assigned) != 1 || len(res.Vipps.Payouts.Cleared) != 1 {
		t.Fatalf("first run = %+v / %+v / %+v, want every row booked", res, res.Vipps.Orders, res.Vipps.AutoAssign)
	}

	who := f.club.Treasurer.ID
	if _, _, err := f.svc.UnassignVippsCharge(ctx, f.club.ID, who, charge); err != nil {
		t.Fatalf("undo charge: %v", err)
	}
	if _, err := f.svc.VoidLedgerEntry(ctx, f.club.ID, rowEntry(t, f.club, orderCharge), who); err != nil {
		t.Fatalf("void order charge: %v", err)
	}
	if _, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, who, kid); err != nil {
		t.Fatalf("undo KID row: %v", err)
	}
	if _, err := f.svc.UnassignBankRowReported(ctx, f.club.ID, who, deposit); err != nil {
		t.Fatalf("undo deposit: %v", err)
	}
	if _, err := f.svc.UnassignBankRowReported(ctx, f.club.ID, who, out); err != nil {
		t.Fatalf("undo transfer: %v", err)
	}
	if _, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, who, payout.bankRow); err != nil {
		t.Fatalf("Angre innbetalingen: %v", err)
	}
	runsBefore := len(f.autoRuns(t))

	res = f.runAuto(t, nil)
	if res.Changed() {
		t.Errorf("second run changed something: %+v", res)
	}
	for _, r := range []string{charge, orderCharge} {
		if rowEntry(t, f.club, r) != "" {
			t.Errorf("declined charge %s booked again", r)
		}
	}
	for _, r := range []string{kid, deposit, out, in, payout.bankRow} {
		if f.bankRowEntry(t, r) != "" {
			t.Errorf("declined bank row %s booked again", r)
		}
	}
	// Bank auto-assign reads the declined deposit and the declined incoming
	// transfer row (both KID-less deposits to it) and counts both.
	if res.Vipps.Orders.DeclinedSkipped != 1 || res.Vipps.AutoAssign.DeclinedSkipped != 1 ||
		res.BankAutoAssign.DeclinedSkipped != 2 || res.Vipps.Payouts.DeclinedSkipped != 1 {
		t.Errorf("declined counts: orders %d, auto-assign %d, bank %d, payouts %d; want 1, 1, 2, 1",
			res.Vipps.Orders.DeclinedSkipped, res.Vipps.AutoAssign.DeclinedSkipped,
			res.BankAutoAssign.DeclinedSkipped, res.Vipps.Payouts.DeclinedSkipped)
	}
	if n := len(f.autoRuns(t)); n != runsBefore {
		t.Errorf("a run that changed nothing wrote %d summary rows", n-runsBefore)
	}
}

// A second run with nothing new makes no entries and writes no summary row.
func TestRunAutomaticBookingIdempotent(t *testing.T) {
	f := newSlotFixture(t)
	f.autoOn(t)
	f.settlement(t, "5000601", 4, 1, true)
	f.kidRow(t, time.Date(f.club.Year, 6, 1, 0, 0, 0, 0, time.UTC), 30000)
	f.transferPair(t, 50000, 5)
	if res := f.runAuto(t, nil); !res.Changed() {
		t.Fatalf("first run = %+v, want bookings", res)
	}
	entries, runs := f.entries(t), len(f.autoRuns(t))

	res := f.runAuto(t, nil)
	if res.Changed() || res.NeedsAudit() || f.entries(t) != entries || len(f.autoRuns(t)) != runs {
		t.Errorf("second run = %+v; entries %d→%d, summary rows %d→%d; want nothing new",
			res, entries, f.entries(t), runs, len(f.autoRuns(t)))
	}
}

// A held Vipps booking slot makes the run skip its Vipps step after its own
// (shortened) wait, with busy in the summary; the bank passes still run.
func TestRunAutomaticBookingBusyWritesRow(t *testing.T) {
	f := newSlotFixture(t)
	f.autoOn(t)
	s := f.settlement(t, "5000701", 6, 1, true)
	kid, _ := f.kidRow(t, time.Date(f.club.Year, 6, 1, 0, 0, 0, 0, time.UTC), 30000)
	f.svc.SetAutoBookingSlotWait(50 * time.Millisecond)
	f.holdSlot(t)

	res := f.runAuto(t, nil)
	if !res.Busy || res.Vipps != nil || res.Failed() {
		t.Fatalf("result = %+v, want busy, no Vipps step and not failed", res)
	}
	if f.bankRowEntry(t, kid) == "" || res.KIDMatched != 1 {
		t.Error("the bank passes did not run while the slot was busy")
	}
	if f.bookedCharges(t, s) != 0 {
		t.Error("a charge was booked while the slot was busy")
	}
	runs := f.autoRuns(t)
	if len(runs) != 1 || runs[0].details["busy"] != true {
		t.Fatalf("summary rows = %+v, want one with busy", runs)
	}
}

// A run cancelled while a transfer pair is waiting on a row lock stops,
// writes its summary row with aborted, and leaves no half-booked item: every
// transfer draft has both rows, and the blocked pair has none.
func TestRunAutomaticBookingCancelledMidRun(t *testing.T) {
	f := newSlotFixture(t)
	f.autoOn(t)
	s := f.settlement(t, "5000801", 7, 1, true)
	f.transferPair(t, 40000, 8)
	blockedOut, blockedIn := f.transferPair(t, 60000, 9)
	lock := testutil.HoldRowLock(t, f.club.Pool, `SELECT id FROM bank_import_rows WHERE id = $1 FOR UPDATE`, blockedIn)

	res, _ := runCancelledWhileBlocked(t, lock, func(ctx context.Context) (*AutoBookingResult, error) {
		return f.svc.RunAutomaticBooking(ctx, f.club.ID, AutoBookingOpts{Trigger: AutoBookingTriggerNightly, Accounting: true}), nil
	})
	if !res.Aborted || !res.Failed() || res.Vipps != nil {
		t.Fatalf("result = %+v, want aborted before the Vipps step", res)
	}
	if f.bankRowEntry(t, blockedOut) != "" || f.bankRowEntry(t, blockedIn) != "" {
		t.Error("the blocked transfer pair was linked")
	}
	if n := f.count(t,
		`SELECT count(*) FROM journal_entries je
		  WHERE je.club_id = $1 AND je.status = 'draft'
		    AND (SELECT count(*) FROM bank_import_rows b WHERE b.journal_entry_id = je.id) <> 2`, f.club.ID); n != 0 {
		t.Errorf("%d transfer drafts without both rows", n)
	}
	if f.bookedCharges(t, s) != 0 {
		t.Error("a charge was booked after the run was cancelled")
	}
	runs := f.autoRuns(t)
	if len(runs) != 1 || runs[0].details["aborted"] != true {
		t.Fatalf("summary rows = %+v, want one with aborted", runs)
	}

	// Which of the two pairs the cancelled run reached first is up to the
	// candidate order; the next run books whatever it left, so both pairs end
	// up as exactly one draft each with both rows, and nothing twice.
	if res := f.runAuto(t, nil); res.Aborted || res.Failed() {
		t.Fatalf("follow-up run = %+v", res)
	}
	if f.bankRowEntry(t, blockedOut) == "" || f.bankRowEntry(t, blockedIn) != f.bankRowEntry(t, blockedOut) {
		t.Error("the follow-up run did not pair the formerly blocked transfer")
	}
	if n := f.count(t,
		`SELECT count(*) FROM journal_entries je
		  WHERE je.club_id = $1 AND je.status = 'draft'
		    AND (SELECT count(*) FROM bank_import_rows b WHERE b.journal_entry_id = je.id) = 2`, f.club.ID); n != 2 {
		t.Errorf("%d transfer drafts with both rows after the follow-up run, want 2", n)
	}
	if f.bookedCharges(t, s) != 2 || f.clearedDeposits(t, s) != 1 {
		t.Error("the follow-up run did not book the Vipps settlement")
	}
}

// blockingReport is a Report API that hangs on the funds call until the
// run's context ends, so a test can cancel a run during its ingest.
type blockingReport struct {
	*fakeVippsReport
	started chan struct{}
	once    sync.Once
}

func (b *blockingReport) FundsForDate(ctx context.Context, _, _ string) ([]vipps.FundsEntry, bool, error) {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done()
	return nil, false, ctx.Err()
}

// A run cancelled while its ingest waits on the Report API books nothing and
// still writes its summary row with aborted, as the system actor.
func TestRunAutomaticBookingCancelledDuringIngest(t *testing.T) {
	f := newSlotFixture(t)
	f.autoOn(t)
	s := f.settlement(t, "5000811", 7, 1, true)
	f.clockAfter(t, "2026-06-10")
	src := &blockingReport{fakeVippsReport: newFakeVippsReport("123456"), started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *AutoBookingResult, 1)
	go func() {
		done <- f.svc.RunAutomaticBooking(ctx, f.club.ID, AutoBookingOpts{Source: src, Trigger: AutoBookingTriggerNightly, Accounting: true})
	}()
	select {
	case <-src.started:
	case <-time.After(30 * time.Second):
		t.Fatal("the ingest never reached the Report API")
	}
	cancel()
	var res *AutoBookingResult
	select {
	case res = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the cancelled run did not return")
	}
	if !res.Aborted || !res.IngestFailed || res.Booked || !res.Failed() {
		t.Fatalf("result = %+v, want aborted during the ingest, nothing booked", res)
	}
	if f.entries(t) != 0 || f.bookedCharges(t, s) != 0 {
		t.Error("a run cancelled during its ingest booked something")
	}
	runs := f.autoRuns(t)
	if len(runs) != 1 || runs[0].details["aborted"] != true || runs[0].actor == nil || *runs[0].actor != f.systemActor(t) {
		t.Fatalf("summary rows = %+v, want one aborted row by the system actor", runs)
	}
}

// Waiting payments queue the treasurer's notice and kick the delivery worker.
// (A missing year before the club's first year is history, never reported,
// and one after this year is not told yet, so the test uses a closed year.)
func TestRunAutomaticBookingQueuesFiscalYearNotice(t *testing.T) {
	f := newSlotFixture(t)
	f.autoOn(t)
	kicks := 0
	f.svc.SetDeliveryKick(func() { kicks++ })
	wf := &waitFixture{svc: f.svc, club: f.club, imp: f.bankImp}
	wf.period(t, 2025, "closed", false)
	f.kidRow(t, time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC), 30000)

	res := f.runAuto(t, nil)
	if res.FiscalYearNotices != 1 || kicks != 1 {
		t.Fatalf("notices %d, kicks %d; want 1 and 1", res.FiscalYearNotices, kicks)
	}
	if n := f.count(t, `SELECT count(*) FROM deliveries WHERE club_id = $1 AND kind = 'accounting_fiscal_year_waiting'`, f.club.ID); n != 1 {
		t.Errorf("%d notices queued, want 1", n)
	}
	// Queued once: the next run adds nothing and kicks nobody.
	if res := f.runAuto(t, nil); res.FiscalYearNotices != 0 || kicks != 1 {
		t.Errorf("second run queued %d, kicks %d; want no new notice", res.FiscalYearNotices, kicks)
	}
	if runs := f.autoRuns(t); len(runs) != 0 {
		t.Errorf("%d summary rows for runs that changed nothing", len(runs))
	}
}

// The observer hears a sync success only when every date answered, and one
// AutoBookingRun per run: failed when the system actor cannot be resolved,
// whose summary row then has no actor.
func TestRunAutomaticBookingObserver(t *testing.T) {
	f := newSlotFixture(t)
	f.autoOn(t)
	obs := &fakeAutoObserver{}
	f.svc.SetAutoBookingObserver(obs)
	f.clockAfter(t, "2026-06-10")

	ok := newFakeVippsReport("123456")
	f.runAuto(t, ok)
	if len(ok.fundsCalls) != vippsReportDays || !slices.Contains(ok.fundsCalls, "900/2026-06-10") {
		t.Errorf("funds calls %v, want d-1..d-4 from 2026-06-10", ok.fundsCalls)
	}
	broken := newFakeVippsReport("123456")
	broken.feesErr = errors.New("synthetic outage")
	f.runAuto(t, broken)
	noLedger := newFakeVippsReport("123456")
	noLedger.ledgers = nil
	if res := f.runAuto(t, noLedger); !res.IngestFailed || !res.Ingest.LedgerFailed {
		t.Errorf("no ledger: ingest %+v, want the ledger failure", res.Ingest)
	}

	f.svc.systemActorInsert = func(context.Context, string) error { return errors.New("synthetic insert failure") }
	res := f.runAuto(t, nil)
	if !res.Failed() || len(res.Errors) != 1 {
		t.Fatalf("result = %+v, want the system actor failure", res)
	}
	if !slices.Equal(obs.synced, []string{"123456"}) || !slices.Equal(obs.runs, []bool{false, false, false, true}) {
		t.Errorf("observer synced %v runs %v", obs.synced, obs.runs)
	}
	runs := f.autoRuns(t)
	last := runs[len(runs)-1]
	if last.actor != nil || len(last.details["errors"].([]any)) != 1 {
		t.Errorf("summary row = %+v, want no actor and the error", last)
	}
}

// Accounting off: the run only ingests, and the observer's last run is not
// failed.
func TestRunAutomaticBookingAccountingOff(t *testing.T) {
	f := newSlotFixture(t)
	f.autoOn(t)
	f.settlement(t, "5000901", 8, 1, true)
	obs := &fakeAutoObserver{}
	f.svc.SetAutoBookingObserver(obs)

	res := f.svc.RunAutomaticBooking(context.Background(), f.club.ID, AutoBookingOpts{Trigger: AutoBookingTriggerBoot})
	if res.Booked || f.entries(t) != 0 || len(f.autoRuns(t)) != 0 || !slices.Equal(obs.runs, []bool{false}) {
		t.Errorf("result = %+v, entries %d, observer %v; want ingest only", res, f.entries(t), obs.runs)
	}
}

// A KID booking whose payment back-link was lost (the run ended between the
// commit and the link) is healed by the run's back-link pass.
func TestRunAutomaticBookingHealsALostBackLink(t *testing.T) {
	f := newSlotFixture(t)
	f.autoOn(t)
	_, inv := f.kidRow(t, time.Date(f.club.Year, 6, 1, 0, 0, 0, 0, time.UTC), 30000)
	if res := f.runAuto(t, nil); res.KIDMatched != 1 || !f.invoicePaid(t, inv.ID) {
		t.Fatalf("first run = %+v, want the KID row booked and the faktura paid", res)
	}
	f.exec(t, `UPDATE invoices SET payment_id = NULL WHERE id = $1`, inv.ID)

	res := f.runAuto(t, nil)
	if res.PaymentsLinked != 1 || !f.invoicePaid(t, inv.ID) {
		t.Errorf("second run linked %d, paid %v; want the back-link healed", res.PaymentsLinked, f.invoicePaid(t, inv.ID))
	}
}

// The writes that follow a committed booking (the rule marker and the
// per-item audit rows) still land when the run's context has ended right
// after the commit. The payment back-links use the same postCommitContext;
// TestRunAutomaticBookingHealsALostBackLink covers a lost one.
func TestPostCommitWritesSurviveACancelledContext(t *testing.T) {
	f := newSlotFixture(t)
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	charge := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 160000, MSN: sweepMSN})
	f.svc.markAutoAssignedWith(gone, f.club.ID, f.club.Treasurer.ID, charge, autoAssignRulePriceItem, "3200",
		"00000000-0000-0000-0000-000000000001", map[string]any{"trigger": AutoBookingTriggerNightly})
	if n := f.count(t, `SELECT count(*) FROM vipps_import_rows WHERE id = $1 AND auto_assign_rule = $2`, charge, autoAssignRulePriceItem); n != 1 {
		t.Error("the rule marker was lost to the cancelled context")
	}
	if n := f.count(t, `SELECT count(*) FROM audit_log WHERE action = $1 AND resource_id = $2`, audit.ActionVippsChargeAutoAssigned, charge); n != 1 {
		t.Error("the vipps_charge.auto_assigned row was lost to the cancelled context")
	}

	deposit := f.club.NewBankRow(t, f.bankImp, fin.BankRowOpts{Cents: 10000})
	f.svc.logBankAutoAssigned(gone, f.club.ID, f.club.Treasurer.ID, deposit, "", "", AutoAssignItem{Target: "faktura 1"}, AutoBookingTriggerNightly)
	if n := f.count(t, `SELECT count(*) FROM audit_log WHERE action = $1 AND resource_id = $2`, audit.ActionBankRowAutoAssigned, deposit); n != 1 {
		t.Error("the bank_row.auto_assigned row was lost to the cancelled context")
	}
}

// The operator Report API sync books only on the per-charge model, as the
// CSV upload and Synk Vipps do.
func TestSyncVippsReportLegacyClubDoesNotBook(t *testing.T) {
	f := newSlotFixture(t)
	f.exec(t, `UPDATE clubs SET vipps_per_charge_enabled = false WHERE id = $1`, f.club.ID)
	const date, msn = "2026-06-10", "123456"
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	order := f.club.NewOrderForInvoice(t, inv)
	fake := newFakeVippsReport(msn)
	reportShape(fake, date, msn, "2001001", order.VippsReference, 45000, 1446)

	res, err := f.svc.SyncVippsReport(context.Background(), fake, f.club.ID, f.club.Treasurer.ID, "", date)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Imported != 5 || res.Orders != nil || res.AutoAssign != nil || f.entries(t) != 0 {
		t.Errorf("result = %+v, entries %d; want the rows imported and nothing booked", res, f.entries(t))
	}
}

// Each Vipps booking path writes its own trigger on the auto-assign audit row.
func TestAutoAssignTriggerOnAuditRow(t *testing.T) {
	f := newSlotFixture(t)
	ctx := context.Background()
	triggerOf := func(t *testing.T, row string) string {
		t.Helper()
		var trig *string
		if err := f.club.Pool.QueryRow(ctx,
			`SELECT details->>'trigger' FROM audit_log WHERE action = $1 AND resource_id = $2`,
			audit.ActionVippsChargeAutoAssigned, row).Scan(&trig); err != nil {
			t.Fatalf("audit row for %s: %v", row, err)
		}
		if trig == nil {
			return ""
		}
		return *trig
	}

	upload := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 160000, MSN: sweepMSN})
	if _, err := f.svc.BookVippsQueue(ctx, f.club.ID, f.club.Treasurer.ID, VippsBookingOpts{Trigger: VippsAutoAssignTriggerUpload}); err != nil {
		t.Fatalf("upload booking: %v", err)
	}
	if got := triggerOf(t, upload); got != VippsAutoAssignTriggerUpload {
		t.Errorf("upload trigger = %q", got)
	}

	resync := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 160000, MSN: sweepMSN})
	if _, err := f.svc.ResyncVippsBilags(ctx, f.club.ID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("resync: %v", err)
	}
	if got := triggerOf(t, resync); got != VippsAutoAssignTriggerResync {
		t.Errorf("resync trigger = %q", got)
	}

	const date = "2026-06-12"
	fake := newFakeVippsReport("123456")
	fake.funds[date] = []vipps.FundsEntry{
		{EntryType: "capture", Reference: "synthetic-walk-in", PspReference: "psp-trigger", Amount: 160000, LedgerDate: date, Time: date + "T08:00:00Z"},
	}
	res, err := f.svc.SyncVippsReport(ctx, fake, f.club.ID, f.club.Treasurer.ID, "", date)
	if err != nil || res.AutoAssign == nil || len(res.AutoAssign.Assigned) != 1 {
		t.Fatalf("report sync = %+v (err %v), want the charge auto-assigned", res, err)
	}
	if got := triggerOf(t, res.AutoAssign.Assigned[0].RowID); got != VippsAutoAssignTriggerReportSync {
		t.Errorf("report sync trigger = %q", got)
	}
}

// A dismissed transfer pair (the treasurer booked it by hand and dismissed
// the rows as "Intern overføring") and a refund-paired row are never paired,
// by a person's Synkroniser or by the run.
func TestTransferPairingSkipsDismissedAndRefundPairedRows(t *testing.T) {
	f := newSlotFixture(t)
	f.autoOn(t)
	dOut, dIn := f.transferPair(t, 70000, 10)
	dismiss := `UPDATE bank_import_rows SET dismissed_at = now(), dismissed_by = $2, dismissed_reason = 'internal_transfer' WHERE id = ANY($1::uuid[])`
	f.exec(t, dismiss, []string{dOut, dIn}, f.club.Treasurer.ID)
	oneOut, oneIn := f.transferPair(t, 80000, 11)
	f.exec(t, dismiss, []string{oneIn}, f.club.Treasurer.ID)
	rOut, rIn := f.transferPair(t, 90000, 12)
	f.exec(t, `UPDATE bank_import_rows SET refund_paired_with = $2 WHERE id = $1`, rOut, rIn)
	f.exec(t, `UPDATE bank_import_rows SET refund_paired_with = $2 WHERE id = $1`, rIn, rOut)

	sync, err := f.svc.BankSync(context.Background(), f.club.ID, f.club.Treasurer.ID)
	if err != nil || sync.TransfersLinked != 0 {
		t.Fatalf("person's sync linked %d (err %v), want none", sync.TransfersLinked, err)
	}
	if res := f.runAuto(t, nil); res.TransfersLinked != 0 {
		t.Fatalf("run linked %d, want none", res.TransfersLinked)
	}
	for _, r := range []string{dOut, dIn, oneOut, oneIn, rOut, rIn} {
		if f.bankRowEntry(t, r) != "" {
			t.Errorf("row %s was paired", r)
		}
	}
	if n := f.count(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID); n != 0 {
		t.Errorf("%d entries, want none", n)
	}
}
