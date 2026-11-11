package accounting

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/payments"
)

// Tests of the automatic Stripe run (BRY-570, ACC-13): the switch, the
// system actor, the bounded slot wait, and a refund's credit note issued
// and queued by a webhook-kicked run.

// stripeAutoObserver records the Stripe runs the observer is told about.
type stripeAutoObserver struct {
	mu   sync.Mutex
	runs []string
}

func (o *stripeAutoObserver) VippsReportSynced(string, time.Time) {}

func (o *stripeAutoObserver) AutoBookingRun(provider string, failed bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	state := "ok"
	if failed {
		state = "failed"
	}
	o.runs = append(o.runs, provider+":"+state)
}

// stripeAutoFixture is a stripeFixture with an audit log, a counted
// delivery kick and an observer.
type stripeAutoFixture struct {
	*stripeFixture
	obs   *stripeAutoObserver
	mu    sync.Mutex
	kicks int
}

func newStripeAutoFixture(t *testing.T) *stripeAutoFixture {
	t.Helper()
	f := &stripeAutoFixture{stripeFixture: newStripeFixture(t), obs: &stripeAutoObserver{}}
	f.svc.audit = audit.NewService(f.club.Pool, zerolog.Nop())
	f.svc.SetAutoBookingObserver(f.obs)
	f.svc.SetDeliveryKick(func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.kicks++
	})
	return f
}

func (f *stripeAutoFixture) autoOn(t *testing.T) {
	t.Helper()
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE clubs SET automatic_booking = true WHERE id = $1`, f.club.ID); err != nil {
		t.Fatal(err)
	}
}

func (f *stripeAutoFixture) kickCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.kicks
}

func (f *stripeAutoFixture) run(trigger string) *StripeAutoBookingResult {
	return f.svc.RunAutomaticStripeBooking(context.Background(), f.club.ID, f.src, trigger)
}

func (f *stripeAutoFixture) systemActor(t *testing.T) string {
	t.Helper()
	id, err := f.svc.SystemActor(context.Background(), f.club.ID)
	if err != nil {
		t.Fatalf("SystemActor: %v", err)
	}
	return id
}

func (f *stripeAutoFixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", sql, err)
	}
	return n
}

type auditRow struct {
	actor   *string
	details map[string]any
}

func (f *stripeAutoFixture) auditRows(t *testing.T, action string) []auditRow {
	t.Helper()
	rows, err := f.club.Pool.Query(context.Background(),
		`SELECT actor_id::text, details FROM audit_log WHERE club_id = $1 AND action = $2 ORDER BY created_at, id`,
		f.club.ID, action)
	if err != nil {
		t.Fatalf("audit rows: %v", err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		var raw []byte
		if err := rows.Scan(&r.actor, &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &r.details); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// A ready charge and fee are booked and their payout cleared, all as the
// system actor and marked Automatic; one summary row says what was done.
func TestSyncAndBookStripeBalanceSystemActorBooksAndClears(t *testing.T) {
	f := newStripeAutoFixture(t)
	ctx := context.Background()
	f.autoOn(t)
	order := catalogOrder(t, f.club, map[string]float64{"service": 120})
	created := time.Date(f.club.Year, 6, 8, 10, 0, 0, 0, time.UTC)
	po := f.insertPayout(t, payoutSpec{id: "po_test_auto", amountOre: 11500, arrival: f.day(6, 10)})
	f.member(t, po, stripeBT{id: "txn_test_auto_charge", category: "charge", amountOre: 12000, feeOre: 300, orderID: order, created: created})
	f.member(t, po, stripeBT{id: "txn_test_auto_fee", category: "fee", stripeTyp: "stripe_fee", amountOre: -200, created: created})
	payoutBT := f.insertBT(t, stripeBT{id: "txn_test_auto_po", category: "payout", amountOre: -11500, created: created.AddDate(0, 0, 1)})
	if _, err := f.club.Pool.Exec(ctx, `UPDATE stripe_balance_transactions SET payout_id = $2 WHERE id = $1`, payoutBT, po); err != nil {
		t.Fatal(err)
	}
	row := f.stripeDeposit(t, f.club.NewBankImport(t), f.day(6, 11), 11500, "STRIPE PAYMENTS PAYOUT")

	res := f.run(AutoBookingTriggerNightly)
	if !res.Booked || res.Busy || res.Failed() || res.IngestFailed {
		t.Fatalf("result = %+v, want booked and not failed", res)
	}
	if res.Booking.ChargesBooked != 1 || res.Booking.FeesBooked != 1 || len(res.Payouts.Cleared) != 1 {
		t.Fatalf("booking %+v payouts %+v, want the charge, the fee and the payout", res.Booking, res.Payouts)
	}

	system := f.systemActor(t)
	if n := f.count(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1 AND created_by <> $2`, f.club.ID, system); n != 0 {
		t.Errorf("%d entries not created by the system actor", n)
	}
	for _, entry := range []string{f.bt(t, f.rowOf(t, "txn_test_auto_charge")).entry, bankRowEntry(t, f.club, row)} {
		je, err := f.svc.GetJournalEntry(ctx, f.club.ID, entry)
		if err != nil || !je.Automatic {
			t.Errorf("entry %s = %+v (err %v), want Automatic", entry, je, err)
		}
	}

	runs := f.auditRows(t, audit.ActionAccountingAutoBookingRun)
	if len(runs) != 1 || runs[0].actor == nil || *runs[0].actor != system {
		t.Fatalf("summary rows = %+v, want one by the system actor", runs)
	}
	d := runs[0].details
	st := auditSub(d, "stripe")
	if d["provider"] != "stripe" || d["trigger"] != "nightly" || d["busy"] != false ||
		auditNum(st, "charges_booked") != 1 || auditNum(st, "fees_booked") != 1 {
		t.Errorf("summary details = %v", d)
	}
	if cleared, _ := st["payouts_cleared"].([]any); len(cleared) != 1 || cleared[0] != "po_test_auto" {
		t.Errorf("payouts_cleared = %v", st["payouts_cleared"])
	}
	if len(f.obs.runs) != 1 || f.obs.runs[0] != "stripe:ok" {
		t.Errorf("observer = %v, want one ok Stripe run", f.obs.runs)
	}

	// A second run finds nothing and writes no row.
	if res := f.run(AutoBookingTriggerNightly); res.Changed() || res.NeedsAudit() {
		t.Errorf("second run = %+v, want nothing", res)
	}
	if n := len(f.auditRows(t, audit.ActionAccountingAutoBookingRun)); n != 1 {
		t.Errorf("summary rows after a quiet run = %d, want 1", n)
	}
}

// While a person's sync holds the club's Stripe slot, the automatic run
// waits its bounded time and then records itself busy, having ingested and
// booked nothing.
func TestSyncAndBookStripeBalanceWaitsThenBusy(t *testing.T) {
	f := newStripeAutoFixture(t)
	f.autoOn(t)
	attempt, _ := f.attempt(t, 120, "NOK")
	f.charge("txn_test_busy_charge", 300, 12000, "nok", map[string]string{"attempt_id": attempt}, "pi_test_busy")
	f.svc.SetStripeAutoSlotWait(50 * time.Millisecond)
	release, ok := f.svc.stripeSync.tryAcquire(f.club.ID)
	if !ok {
		t.Fatal("slot not free")
	}
	defer release()

	system := f.systemActor(t)
	start := time.Now()
	_, _, _, err := f.svc.SyncAndBookStripeBalance(context.Background(), f.src, f.club.ID, system, 50*time.Millisecond)
	if !errors.Is(err, ErrStripeSyncBusy) || time.Since(start) < 50*time.Millisecond {
		t.Fatalf("SyncAndBookStripeBalance = %v after %s, want ErrStripeSyncBusy after the wait", err, time.Since(start))
	}

	res := f.run(AutoBookingTriggerStripeEvent)
	if !res.Busy || res.Failed() || res.Booking != nil || res.Ingest != nil {
		t.Fatalf("result = %+v, want busy, not failed, nothing ingested or booked", res)
	}
	if f.src.listCalls != 0 || len(f.txns(t)) != 0 || f.entryCount(t) != 0 {
		t.Errorf("list calls %d rows %d entries %d, want nothing", f.src.listCalls, len(f.txns(t)), f.entryCount(t))
	}
	runs := f.auditRows(t, audit.ActionAccountingAutoBookingRun)
	if len(runs) != 1 || runs[0].details["busy"] != true || runs[0].details["trigger"] != "stripe_event" {
		t.Fatalf("summary rows = %+v, want one busy stripe_event row", runs)
	}
}

// With the club's switch off, a nightly run only ingests and a
// webhook-kicked run does not even call Stripe; neither writes a row.
func TestRunAutomaticStripeBookingSwitchOff(t *testing.T) {
	f := newStripeAutoFixture(t)
	attempt, _ := f.attempt(t, 120, "NOK")
	f.charge("txn_test_off_charge", 300, 12000, "nok", map[string]string{"attempt_id": attempt}, "pi_test_off")

	res := f.run(AutoBookingTriggerStripeEvent)
	if !res.Skipped || res.Booked || res.Failed() || f.src.listCalls != 0 {
		t.Fatalf("event run = %+v (list calls %d), want skipped with no Stripe call", res, f.src.listCalls)
	}

	res = f.run(AutoBookingTriggerNightly)
	if res.Booked || res.Booking != nil || res.Ingest == nil || res.Ingest.Inserted != 1 || res.Failed() {
		t.Fatalf("nightly run = %+v, want ingest only", res)
	}
	if got := f.txns(t)["txn_test_off_charge"]; got.Status != StripeTxnUnbooked {
		t.Errorf("charge = %+v, want stored unbooked", got)
	}
	if n := f.entryCount(t); n != 0 {
		t.Errorf("entries = %d, want none", n)
	}
	if n := len(f.auditRows(t, audit.ActionAccountingAutoBookingRun)); n != 0 {
		t.Errorf("summary rows = %d, want none", n)
	}
	if len(f.obs.runs) != 2 || f.obs.runs[0] != "stripe:ok" || f.obs.runs[1] != "stripe:ok" {
		t.Errorf("observer = %v, want two ok Stripe runs", f.obs.runs)
	}
}

// A refund made in the Stripe Dashboard of a charge already booked: the
// webhook-kicked run books it, issues and books the credit note as the
// system actor, queues it for delivery and kicks the delivery worker once.
func TestRunAutomaticStripeBookingDashboardRefund(t *testing.T) {
	f := newStripeAutoFixture(t)
	ctx := context.Background()
	inv := f.twoLineInvoice(t)
	f.passBookedCharge(t, "txn_test_ad_charge", inv)
	f.autoOn(t)
	f.refundTxn("txn_test_ad_refund", 300, 9000, "txn_test_ad_charge", nil)

	res := f.run(AutoBookingTriggerStripeEvent)
	if res.Failed() || res.Booking == nil || res.Booking.RefundsBooked != 1 {
		t.Fatalf("result = %+v (booking %+v), want the refund booked", res, res.Booking)
	}
	system := f.systemActor(t)
	docs := f.refundDocs(t, f.rowOf(t, "txn_test_ad_refund"))
	if len(docs) != 1 || docs[0].kind != "credit" || docs[0].invoiceID != inv.ID || docs[0].amountOre != 9000 {
		t.Fatalf("documents = %+v, want one 90 kr credit note", docs)
	}
	note := docs[0].creditInvoiceID
	var noteBy string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT created_by::text FROM journal_entries WHERE source = 'invoice_sync' AND source_id = $1::text AND status = 'posted'`,
		note).Scan(&noteBy); err != nil || noteBy != system {
		t.Errorf("credit note entry by %q (%v), want the system actor", noteBy, err)
	}
	if n := f.count(t, `SELECT count(*) FROM deliveries WHERE invoice_id = $1::uuid AND kind = 'invoice_refund_document' AND status = 'pending'`, note); n != 1 {
		t.Errorf("pending refund document deliveries = %d, want 1", n)
	}
	if n := f.kickCount(); n != 1 {
		t.Errorf("delivery kicks = %d, want 1", n)
	}
	issued := f.auditRows(t, audit.ActionInvoiceRefundCreditIssued)
	if len(issued) != 1 || issued[0].actor == nil || *issued[0].actor != system {
		t.Errorf("credit issued rows = %+v, want one by the system actor", issued)
	}
	runs := f.auditRows(t, audit.ActionAccountingAutoBookingRun)
	if len(runs) != 1 || runs[0].details["provider"] != "stripe" || runs[0].details["trigger"] != "stripe_event" ||
		auditNum(auditSub(runs[0].details, "stripe"), "refunds_booked") != 1 {
		t.Fatalf("summary rows = %+v, want one stripe_event row with the refund", runs)
	}
}

// A full refund made with Brygge's button and "Krev inn på nytt", booked by
// a webhook-kicked run: no credit note, the faktura reopens.
func TestRunAutomaticStripeBookingButtonRecollect(t *testing.T) {
	f := newStripeAutoFixture(t)
	inv := f.twoLineInvoice(t)
	charge, _ := f.passBookedCharge(t, "txn_test_ar_charge", inv)
	key := f.requestViaButton(t, charge, "txn_test_ar_refund", 45000, payments.RefundOutcomeRecollect)
	f.autoOn(t)
	f.refundTxn("txn_test_ar_refund", 300, 45000, "txn_test_ar_charge", map[string]string{"refund_request_key": key})

	res := f.run(AutoBookingTriggerStripeEvent)
	if res.Failed() || res.Booking == nil || res.Booking.RefundsBooked != 1 {
		t.Fatalf("result = %+v, want the refund booked", res)
	}
	docs := f.refundDocs(t, f.rowOf(t, "txn_test_ar_refund"))
	if len(docs) != 1 || docs[0].kind != "recollect" || docs[0].creditInvoiceID != "" {
		t.Fatalf("documents = %+v, want one recollect and no credit note", docs)
	}
	if s := f.invoice(t, inv.ID); s.paid || s.credited || !s.owed {
		t.Errorf("faktura = %+v, want an ordinary open faktura", s)
	}
	if n := f.count(t, `SELECT count(*) FROM deliveries WHERE club_id = $1 AND kind = 'invoice_refund_document'`, f.club.ID); n != 0 {
		t.Errorf("refund document deliveries = %d, want none", n)
	}
	if n := len(f.auditRows(t, audit.ActionInvoiceRefundCreditIssued)); n != 0 {
		t.Errorf("credit issued rows = %d, want none", n)
	}
}

// The endpoint's sync still answers busy at once, and books as the person.
func TestTrySyncAndBookStripeBalanceUnchanged(t *testing.T) {
	f := newStripeAutoFixture(t)
	f.autoOn(t)
	release, ok := f.svc.stripeSync.tryAcquire(f.club.ID)
	if !ok {
		t.Fatal("slot not free")
	}
	done := make(chan error, 1)
	go func() {
		_, _, _, err := f.svc.TrySyncAndBookStripeBalance(context.Background(), f.src, f.club.ID, f.club.Treasurer.ID)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrStripeSyncBusy) {
			t.Fatalf("while held: %v, want ErrStripeSyncBusy", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TrySyncAndBookStripeBalance waited for the slot")
	}
	release()

	attempt, _ := f.attempt(t, 120, "NOK")
	f.charge("txn_test_try_charge", 300, 12000, "nok", map[string]string{"attempt_id": attempt}, "pi_test_try")
	if _, _, _, err := f.svc.TrySyncAndBookStripeBalance(context.Background(), f.src, f.club.ID, ""); err == nil {
		t.Error("no acting user was accepted")
	}
	synced, booked, payouts, err := f.svc.TrySyncAndBookStripeBalance(context.Background(), f.src, f.club.ID, f.club.Treasurer.ID)
	if err != nil || synced.Inserted != 1 || booked == nil || payouts == nil {
		t.Fatalf("free slot = %+v %+v %+v %v", synced, booked, payouts, err)
	}
	if n := len(f.auditRows(t, audit.ActionAccountingAutoBookingRun)); n != 0 {
		t.Errorf("a person's sync wrote %d automatic summary rows", n)
	}
}

// A kicked run that fails the same way as the club's previous run writes no
// second summary row; a boot or nightly run always writes its row.
func TestRunAutomaticStripeBookingRepeatedFailureOneRow(t *testing.T) {
	f := newStripeAutoFixture(t)
	f.autoOn(t)
	f.svc.SetStripeAutoSlotWait(20 * time.Millisecond)
	release, ok := f.svc.stripeSync.tryAcquire(f.club.ID)
	if !ok {
		t.Fatal("slot not free")
	}
	defer release()

	for range 3 {
		if res := f.run(AutoBookingTriggerStripeEvent); !res.Busy {
			t.Fatalf("result = %+v, want busy", res)
		}
	}
	if n := len(f.auditRows(t, audit.ActionAccountingAutoBookingRun)); n != 1 {
		t.Fatalf("summary rows after three busy kicked runs = %d, want 1", n)
	}
	f.run(AutoBookingTriggerNightly)
	runs := f.auditRows(t, audit.ActionAccountingAutoBookingRun)
	if len(runs) != 2 || runs[1].details["trigger"] != "nightly" || runs[1].details["busy"] != true {
		t.Fatalf("summary rows = %+v, want the nightly busy row too", runs)
	}
}
