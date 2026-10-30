package accounting

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// An order-booked charge is undone by voiding its bilag in the ledger. The
// void releases the charge to the Vipps queue and reopens the fakturas its
// order paid; left linked to the voided entry it would be in no queue and its
// settlement could never clear.

func (f *payoutSweepFixture) invoicePaid(t *testing.T, id string) bool {
	t.Helper()
	var paid bool
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT payment_id IS NOT NULL FROM invoices WHERE id = $1`, id).Scan(&paid); err != nil {
		t.Fatalf("read invoice: %v", err)
	}
	return paid
}

// orderSettlement is a two-charge settlement whose first charge names a
// checkout order paying a faktura of the same amount.
func (f *payoutSweepFixture) orderSettlement(t *testing.T, number string, day int, withDeposit bool) (sweepSettlement, fin.Invoice) {
	t.Helper()
	m := f.club.NewMember(t, "member")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	order := f.club.NewOrderForInvoice(t, inv)
	s := f.settlement(t, number, day, []int64{45000, 25000}, 2000, withDeposit)
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE vipps_import_rows SET order_id = $2 WHERE id = $1`, s.charges[0], order.VippsReference); err != nil {
		t.Fatalf("name the order: %v", err)
	}
	return s, inv
}

func (f *payoutSweepFixture) bookOrderCharge(t *testing.T, rowID string) {
	t.Helper()
	if _, found, err := f.svc.AssignVippsChargeToOrder(context.Background(), f.club.ID, rowID, f.club.Treasurer.ID); err != nil || !found {
		t.Fatalf("book the order charge: found=%v err=%v", found, err)
	}
}

func TestLedgerVoidOfAnOrderChargeReleasesItAndReopensTheFaktura(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	s, inv := f.orderSettlement(t, "3600101", 2, true)
	f.bookOrderCharge(t, s.charges[0])
	f.book(t, s.charges[1])
	if !f.invoicePaid(t, inv.ID) {
		t.Fatal("the order did not settle its faktura")
	}
	entry := *f.chargeEntry(t, s.charges[0])

	if _, err := f.svc.VoidLedgerEntry(ctx, f.club.ID, entry, f.club.Treasurer.ID); err != nil {
		t.Fatalf("void: %v", err)
	}
	if got := f.entryStatusOf(t, entry); got != "voided" {
		t.Errorf("entry is %s, want voided", got)
	}
	if f.chargeEntry(t, s.charges[0]) != nil {
		t.Errorf("the voided charge is still linked: it is in no queue")
	}
	var queued bool
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT journal_entry_id IS NULL AND dismissed_at IS NULL FROM vipps_import_rows WHERE id = $1`, s.charges[0]).Scan(&queued); err != nil || !queued {
		t.Errorf("charge is not back in the Vipps queue (queued=%v, err=%v)", queued, err)
	}
	if f.invoicePaid(t, inv.ID) {
		t.Errorf("the order's faktura is still paid after the void")
	}

	// The settlement waits for the charge, then clears once it is rebooked.
	res, err := f.svc.ClearVippsPayout(ctx, f.club.ID, s.bankRow, f.club.Treasurer.ID)
	if err != nil || res.Ready || res.UnreconciledCharges != 1 {
		t.Fatalf("clear with the released charge = %+v (err %v), want it to wait", res, err)
	}
	f.bookOrderCharge(t, s.charges[0])
	if !f.invoicePaid(t, inv.ID) {
		t.Errorf("rebooking the order charge did not settle its faktura")
	}
	f.clear(t, s)
	if got := f.clearingNet(t); !floatNear(got, 0, 0.005) {
		t.Errorf("2900 net = %.2f, want 0", got)
	}
}

// A charge a void from before the release left linked to a voided entry is
// released by the plain Angre, which also reopens the order's fakturas.
func TestUnassignReleasesAChargeLeftOnAVoidedOrderEntry(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	s, inv := f.orderSettlement(t, "3600201", 2, false)
	f.bookOrderCharge(t, s.charges[0])
	if _, err := f.svc.VoidJournalEntry(ctx, f.club.ID, *f.chargeEntry(t, s.charges[0]), f.club.Treasurer.ID); err != nil {
		t.Fatalf("void: %v", err)
	}
	if !f.invoicePaid(t, inv.ID) || f.chargeEntry(t, s.charges[0]) == nil {
		t.Fatal("setup: the charge should be linked to a voided entry with its faktura still paid")
	}

	if err := f.unassign(s.charges[0]); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if f.chargeEntry(t, s.charges[0]) != nil || f.invoicePaid(t, inv.ID) {
		t.Errorf("the release left the charge linked or the faktura paid")
	}
}

// A settlement's fee entry cannot be voided from the ledger: the fee rows
// would stay linked to a voided entry and the fee never be booked again. Its
// payout entry is bank-linked and refused as such.
func TestLedgerVoidRefusesASettlementsFeeAndPayoutEntries(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	s := f.settlement(t, "3600301", 2, []int64{40000}, 1500, true)
	f.book(t, s.charges...)
	f.clear(t, s)
	fee, bank := f.feeEntry(t, s), *f.bankEntry(t, s.bankRow)

	_, err := f.svc.VoidLedgerEntry(ctx, f.club.ID, fee, f.club.Treasurer.ID)
	var settlementErr *VippsSettlementEntryError
	if !errors.Is(err, ErrVippsSettlementEntry) || !errors.As(err, &settlementErr) || settlementErr.RowType != "fee" {
		t.Fatalf("void of the fee entry: err = %v, want VippsSettlementEntryError for a fee", err)
	}
	if !strings.Contains(err.Error(), "ingen angre") {
		t.Errorf("message %q does not say there is no undo for the fee", err.Error())
	}
	if _, err := f.svc.VoidLedgerEntry(ctx, f.club.ID, bank, f.club.Treasurer.ID); !errors.Is(err, ErrBankLinkedEntry) {
		t.Errorf("void of the payout entry: err = %v, want ErrBankLinkedEntry", err)
	}
	for _, id := range []string{fee, bank} {
		if got := f.entryStatusOf(t, id); got != "posted" {
			t.Errorf("entry %s is %s after a refused void, want posted", id, got)
		}
	}
	if got := f.clearingNet(t); !floatNear(got, 0, 0.005) {
		t.Errorf("2900 net = %.2f, want 0", got)
	}
}

// An apply trusts the database, not its own bookkeeping: a charge it posted
// and that was unassigned before its settlement's turn keeps the settlement's
// fee and payout drafts unposted.
func TestBacklogApplyRechecksChargesBeforePostingASettlement(t *testing.T) {
	f := newPayoutSweepFixture(t)
	s := f.settlement(t, "3600401", 2, []int64{40000, 25000}, 2000, true)
	f.book(t, s.charges...)
	f.clear(t, s)
	fee, bank, charge := f.feeEntry(t, s), *f.bankEntry(t, s.bankRow), *f.chargeEntry(t, s.charges[1])
	f.toDraft(t, fee, bank, charge)

	backlogBeforeSettlementsHook = func() {
		if err := f.unassign(s.charges[1]); err != nil {
			t.Errorf("unassign between the charge post and the settlement: %v", err)
		}
	}
	t.Cleanup(func() { backlogBeforeSettlementsHook = nil })

	res := f.backlog(t, true)
	if got := f.entryStatusOf(t, charge); got != "voided" {
		t.Fatalf("setup: the charge draft was %s, want it posted and then voided", got)
	}
	for _, id := range []string{fee, bank} {
		if got := f.entryStatusOf(t, id); got != "draft" {
			t.Errorf("settlement draft %s is %s, want it left as a draft", id, got)
		}
	}
	if len(res.Refused) != 2 || !strings.Contains(res.Refused[0].Reason, "not posted") {
		t.Errorf("refused = %+v, want both settlement drafts refused as not posted", res.Refused)
	}
}

// Undoing a deposit takes the settlement's payout rows before its entries,
// the order ClearVippsPayout and the backlog posting take them in. Here a
// transaction plays that second side: it holds the payout rows and then wants
// the deposit's entry. If the unassign locked the entry first the two would
// wait on each other and Postgres would abort one of them.
func TestUnassignDepositLocksPayoutRowsBeforeItsEntry(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	s := f.settlement(t, "3600501", 2, []int64{40000, 25000}, 2000, true)
	f.book(t, s.charges...)
	f.clear(t, s)
	bankEntry := *f.bankEntry(t, s.bankRow)

	tx, err := f.svc.db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := lockSettlementPayoutsTx(ctx, tx, f.club.ID, sweepMSN, s.booking); err != nil {
		t.Fatalf("lock the settlement: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, s.bankRow)
		done <- err
	}()
	// Wait until the unassign is queued on a lock (the payout rows we hold).
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := f.club.Pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatalf("poll locks: %v", err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the unassign never queued on the settlement lock")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The side holding the payout rows now takes the entry. It must not wait
	// on the unassign, which has to be waiting on us without holding it.
	lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := tx.Exec(lockCtx, `SELECT 1 FROM journal_entries WHERE id = $1 FOR UPDATE`, bankEntry); err != nil {
		t.Fatalf("locking the deposit's entry while holding the payout rows: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if f.bankEntry(t, s.bankRow) != nil {
		t.Errorf("the deposit is still booked after its unassign")
	}
}
