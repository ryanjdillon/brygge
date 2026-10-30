package accounting

import (
	"bytes"
	"compress/zlib"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// The Vipps and refund lists leave a reference year out, as Tildel does.
func TestVippsAndRefundListsLeaveReferenceYearOut(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()
	imp := f.club.NewBankImport(t)

	f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 80000, Date: f.refDay(6, 1), CustomerName: "Synthetic Payer"})
	f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 90000, Date: f.curDay(6, 1), CustomerName: "Synthetic Payer"})
	f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(7, 1), Cents: 100000, Description: "Utb. 5 Vippsnr 000001"})
	f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.curDay(7, 1), Cents: 110000, Description: "Utb. 6 Vippsnr 000001"})
	dismissRefund := func(date time.Time, cents int64) string {
		id := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: date, Cents: cents})
		f.exec(t, `UPDATE bank_import_rows SET dismissed_at = now(), dismissed_by = $2, dismissed_reason = 'overpayment' WHERE id = $1`,
			id, f.club.Treasurer.ID)
		return id
	}
	dismissRefund(f.refDay(8, 1), 50000)
	dismissRefund(f.curDay(8, 1), 60000)
	inbound := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.curDay(9, 1), Cents: 20000})
	f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(9, 1), Cents: -20000})
	f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.curDay(9, 2), Cents: -20000})

	counts := func() [6]int {
		t.Helper()
		queue, err := f.svc.ListVippsQueue(ctx, f.club.ID, 100)
		if err != nil {
			t.Fatalf("vipps queue: %v", err)
		}
		charges, err := f.svc.ListUnreconciledVippsCharges(ctx, f.club.ID)
		if err != nil {
			t.Fatalf("vipps charges: %v", err)
		}
		missing, err := f.svc.ListMissingVippsSettlements(ctx, f.club.ID)
		if err != nil {
			t.Fatalf("missing settlements: %v", err)
		}
		pending, err := f.svc.ListPendingRefunds(ctx, f.club.ID)
		if err != nil {
			t.Fatalf("pending refunds: %v", err)
		}
		n, err := f.svc.CountPendingRefunds(ctx, f.club.ID)
		if err != nil {
			t.Fatalf("pending refund count: %v", err)
		}
		cands, err := f.svc.SuggestRefundOutbound(ctx, f.club.ID, inbound)
		if err != nil {
			t.Fatalf("outbound candidates: %v", err)
		}
		if n != len(pending) {
			t.Errorf("pending refund count %d disagrees with its list of %d", n, len(pending))
		}
		return [6]int{len(queue), len(charges), len(missing), len(pending), n, len(cands)}
	}

	if got := counts(); got != [6]int{2, 2, 2, 2, 2, 2} {
		t.Fatalf("unmarked lists = %v, want every row listed", got)
	}
	f.mark(t, true)
	if got := counts(); got != [6]int{1, 1, 1, 1, 1, 1} {
		t.Errorf("reference-year lists = %v, want only the current year's row in each", got)
	}
}

// A charge dated in a reference year is not undone, so it cannot re-enter a
// queue that hides it.
func TestUnassignVippsChargeRefusesReferenceYear(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()
	charge := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 80000, Date: f.refDay(6, 1), CustomerName: "Synthetic Payer"})
	res, err := f.svc.CreateVippsChargeBilag(ctx, f.club.ID, charge, "3200", "", f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("book charge: %v", err)
	}
	f.mark(t, true)

	if _, _, err := f.svc.UnassignVippsCharge(ctx, f.club.ID, f.club.Treasurer.ID, charge); !errors.Is(err, ErrReferencePeriod) {
		t.Fatalf("unassign a reference-year charge: err = %v, want ErrReferencePeriod", err)
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE id = $1 AND status <> 'voided'`, res.EntryID); n != 1 {
		t.Errorf("the refused unassign touched the entry")
	}
}

// A charge dated in a reference year is refused whatever its entry's state:
// the unassign would reopen the fakturas it settled, and a reference-year
// faktura must not reappear as open. The ledger void is refused by the guard
// before it releases anything.
func TestReferenceYearChargeIsNeverReleasedOrReopened(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()
	member := f.club.NewMember(t, "member")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &member, IssueDate: f.refDay(2, 1), Cents: 45000})

	// One charge on a voided entry, one on a posted entry; both settle the faktura.
	voided := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, Date: f.refDay(6, 1), CustomerName: "Synthetic Payer"})
	if _, err := f.svc.AssignVippsChargeToInvoice(ctx, f.club.ID, voided, inv.ID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("assign charge to faktura: %v", err)
	}
	var entryID string
	if err := f.club.Pool.QueryRow(ctx, `SELECT journal_entry_id::text FROM vipps_import_rows WHERE id = $1`, voided).Scan(&entryID); err != nil {
		t.Fatalf("charge entry: %v", err)
	}
	f.mark(t, true)

	// The ledger void: the guard refuses (the entry is on the clearing account) before the release.
	if _, err := f.svc.VoidLedgerEntry(ctx, f.club.ID, entryID, f.club.Treasurer.ID); !errors.Is(err, ErrReferencePeriod) {
		t.Fatalf("ledger void of a reference-year charge: err = %v, want ErrReferencePeriod", err)
	}
	if _, _, err := f.svc.UnassignVippsCharge(ctx, f.club.ID, f.club.Treasurer.ID, voided); !errors.Is(err, ErrReferencePeriod) {
		t.Fatalf("unassign a reference-year charge: err = %v, want ErrReferencePeriod", err)
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE id = $1 AND status = 'posted'`, entryID); n != 1 {
		t.Errorf("a refused void changed the entry")
	}
	if n := f.scalar(t, `SELECT count(*) FROM vipps_import_rows WHERE id = $1 AND journal_entry_id = $2 AND invoice_id = $3`, voided, entryID, inv.ID); n != 1 {
		t.Errorf("a refused void released the charge")
	}
	if n := f.scalar(t, `SELECT count(*) FROM invoices WHERE id = $1 AND payment_id IS NOT NULL`, inv.ID); n != 1 {
		t.Errorf("the reference-year faktura was reopened")
	}

	// A charge whose entry was voided before the year was marked is refused too.
	f.mark(t, false)
	other := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, Date: f.refDay(6, 2), CustomerName: "Synthetic Payer"})
	res, err := f.svc.CreateVippsChargeBilag(ctx, f.club.ID, other, "3200", "", f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("book charge: %v", err)
	}
	if _, err := f.svc.VoidJournalEntry(ctx, f.club.ID, res.EntryID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("void: %v", err)
	}
	f.mark(t, true)
	if _, _, err := f.svc.UnassignVippsCharge(ctx, f.club.ID, f.club.Treasurer.ID, other); !errors.Is(err, ErrReferencePeriod) {
		t.Errorf("unassign a reference charge on a voided entry: err = %v, want ErrReferencePeriod", err)
	}
}

// The Vipps charge passes do not try a reference-year charge on every sync.
func TestVippsChargePassesSkipReferenceYear(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()
	f.exec(t, `UPDATE clubs SET vipps_per_charge_enabled = true, vipps_auto_assign = true WHERE id = $1`, f.club.ID)
	member := f.club.NewMember(t, "member")
	f.exec(t, `UPDATE users SET first_name = 'Synthetic', last_name = 'Kaiplass' WHERE id = $1`, member.ID)
	f.club.NewInvoice(t, fin.InvoiceOpts{Member: &member, IssueDate: f.refDay(2, 1),
		Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, Date: f.refDay(6, 1), CustomerName: "Synthetic Kaiplass"})
	var orderID string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO orders (club_id, total_amount, token, status, vipps_reference) VALUES ($1, 300, $2, 'paid', 'chk-synthetic-1') RETURNING id`,
		f.club.ID, "tok-"+testutil.RandomHex(8)).Scan(&orderID); err != nil {
		t.Fatalf("order: %v", err)
	}
	f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, Date: f.refDay(6, 2), OrderID: "chk-synthetic-1", CustomerName: "Synthetic Orderer"})

	run := func() (auto *AutoAssignResult, orders *VippsOrderReconcileResult) {
		t.Helper()
		orders, err := f.svc.AutoReconcileOrderCharges(ctx, f.club.ID, f.club.Treasurer.ID)
		if err != nil {
			t.Fatalf("order charges: %v", err)
		}
		auto, err = f.svc.AutoAssignVippsCharges(ctx, f.club.ID, f.club.Treasurer.ID)
		if err != nil {
			t.Fatalf("auto-assign: %v", err)
		}
		return auto, orders
	}

	f.mark(t, true)
	auto, orders := run()
	if auto.NeedsReview != 0 || len(auto.Assigned) != 0 || orders.Booked != 0 || len(orders.SkippedDetails) != 0 {
		t.Errorf("reference year: auto %+v, orders %+v; want neither pass to look at the charges", auto, orders)
	}

	f.mark(t, false)
	auto, orders = run()
	if auto.NeedsReview+len(auto.Assigned) == 0 || orders.Booked+len(orders.SkippedDetails) == 0 {
		t.Errorf("unmarked: auto %+v, orders %+v; want both passes to see the charges", auto, orders)
	}
}

// A settlement booked in a reference year whose deposit arrived in the next
// is skipped with a reason pointing to the opening balance, never failed, and
// books nothing. A reference-year payout with its deposit is not looked at.
func TestVippsPayoutSweepLeavesReferenceSettlements(t *testing.T) {
	f := newPayoutSweepFixture(t)
	ctx := context.Background()
	refYear := f.club.Year - 1
	period := addPeriod(t, f.club, refYear)

	crossing := f.settlementOn(t, "9", time.Date(refYear, 12, 31, 0, 0, 0, 0, time.UTC), []int64{50000, 30000}, 1000, true)
	inside := f.settlementOn(t, "8", time.Date(refYear, 12, 10, 0, 0, 0, 0, time.UTC), []int64{20000}, 500, true)
	if _, err := f.club.Pool.Exec(ctx,
		`UPDATE vipps_import_rows SET dismissed_at = now() WHERE club_id = $1 AND row_type = 'belastning'`, f.club.ID); err != nil {
		t.Fatalf("dismiss charges: %v", err)
	}
	if _, _, err := f.svc.SetPeriodReference(ctx, f.club.ID, period, true); err != nil {
		t.Fatalf("mark: %v", err)
	}

	res, err := f.svc.ClearReadyVippsPayouts(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(res.Cleared) != 0 || len(res.Failed) != 0 || len(res.Waiting) != 0 {
		t.Errorf("sweep = %+v, want nothing cleared, failed or waiting", res)
	}
	skipped := settlementsOf(res.Skipped)
	if len(skipped) != 1 || !strings.Contains(skipped["9"], "referanseår") {
		t.Errorf("skipped = %v, want only the crossing settlement 9, with the reference reason", skipped)
	}
	// The same crossing settlement with charges that are reference drafts (they
	// never post) is skipped with the reference reason, not left waiting.
	if _, err := f.club.Pool.Exec(ctx,
		`UPDATE vipps_import_rows SET dismissed_at = NULL WHERE club_id = $1 AND row_type = 'belastning'`, f.club.ID); err != nil {
		t.Fatalf("undismiss charges: %v", err)
	}
	res, err = f.svc.ClearReadyVippsPayouts(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("sweep with unposted charges: %v", err)
	}
	if len(res.Waiting) != 0 || len(res.Failed) != 0 || len(settlementsOf(res.Skipped)) != 1 ||
		!strings.Contains(settlementsOf(res.Skipped)["9"], "referanseår") {
		t.Errorf("sweep with unposted charges = %+v, want settlement 9 skipped with the reference reason and nothing waiting", res)
	}
	if f.bankEntry(t, crossing.bankRow) != nil || f.bankEntry(t, inside.bankRow) != nil {
		t.Errorf("a reference-year settlement's deposit was booked")
	}
	if n := f.postedEntries(t); n != 0 {
		t.Errorf("the sweep left %d entries behind", n)
	}
}

// The legacy single-bilag reconcile does not take a reference year's deposits.
func TestLegacyVippsReconcileSkipsReferenceDeposit(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()
	f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 80000, Date: f.refDay(6, 1), MSN: "000001", CustomerName: "Synthetic Payer"})
	f.club.NewVippsRow(t, fin.VippsRowOpts{RowType: "payout", Cents: -80000, Date: f.refDay(6, 1), MSN: "000001", Settlement: "7"})
	imp := f.club.NewBankImport(t)
	f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(6, 2), Cents: 80000, Description: "Utb. 7 Vippsnr 000001"})

	f.mark(t, true)
	matched, unbalanced, _, err := f.svc.syncVippsReconciliations(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil || matched != 0 || unbalanced != 0 {
		t.Errorf("reference year: matched %d, unbalanced %d (%v); want the deposit not to be a candidate", matched, unbalanced, err)
	}

	f.mark(t, false)
	matched, unbalanced, _, err = f.svc.syncVippsReconciliations(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil || matched+unbalanced == 0 {
		t.Errorf("unmarked: matched %d, unbalanced %d (%v); want the deposit to be a candidate", matched, unbalanced, err)
	}
}

// The Stripe booking pass defers a charge dated in a reference year with its
// own reason and writes nothing.
func TestStripeBookingDefersReferenceYear(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	refYear := f.club.Year - 1
	period := addPeriod(t, f.club, refYear)
	order := catalogOrder(t, f.club, map[string]float64{"service": 120})
	f.insertBT(t, stripeBT{id: "txn_test_ref_charge", category: "charge", amountOre: 12000, feeOre: 300, orderID: order,
		created: time.Date(refYear, 6, 8, 10, 0, 0, 0, time.UTC)})
	if _, _, err := f.svc.SetPeriodReference(ctx, f.club.ID, period, true); err != nil {
		t.Fatalf("mark: %v", err)
	}

	res, err := f.svc.BookStripeBalance(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("book: %v", err)
	}
	if res.ChargesBooked != 0 || len(res.Deferred) != 1 || res.Deferred[0].Reason != StripeDeferReferencePeriod || len(res.Errors) != 0 {
		t.Fatalf("result = %+v, want the charge deferred as %s and no error", res, StripeDeferReferencePeriod)
	}
	var n int
	if err := f.club.Pool.QueryRow(ctx, `SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d entries written (%v), want none", n, err)
	}

	if _, _, err := f.svc.SetPeriodReference(ctx, f.club.ID, period, false); err != nil {
		t.Fatalf("unmark: %v", err)
	}
	if res, err := f.svc.BookStripeBalance(ctx, f.club.ID, f.club.Treasurer.ID); err != nil || res.ChargesBooked != 1 {
		t.Errorf("unmarked: %+v (%v), want the charge booked", res, err)
	}
}

// Whole-period operations refuse a reference period up front.
func TestWholePeriodOperationsRefuseReferencePeriod(t *testing.T) {
	f := newRefFixture(t, true)
	ctx := context.Background()
	actor := f.club.Treasurer.ID
	imp := f.club.NewBankImport(t)

	checks := map[string]func() error{
		"sync payments": func() error { _, err := f.svc.SyncPayments(ctx, f.club.ID, f.refPeriod, actor); return err },
		"sync invoices": func() error { _, err := f.svc.SyncInvoices(ctx, f.club.ID, f.refPeriod, actor); return err },
		"resplit preview": func() error {
			_, err := f.svc.ResplitInvoiceBilags(ctx, f.club.ID, f.refPeriod, actor, false)
			return err
		},
		"resplit apply": func() error {
			_, err := f.svc.ResplitInvoiceBilags(ctx, f.club.ID, f.refPeriod, actor, true)
			return err
		},
		"vipps backlog preview": func() error {
			_, err := f.svc.PostVippsDraftBacklog(ctx, f.club.ID, f.refPeriod, actor, false, nil)
			return err
		},
		"vipps backlog apply": func() error {
			_, err := f.svc.PostVippsDraftBacklog(ctx, f.club.ID, f.refPeriod, actor, true, nil)
			return err
		},
		"auto-match": func() error { _, err := f.svc.AutoMatchImport(ctx, f.club.ID, imp, f.refPeriod, actor); return err },
	}
	for name, run := range checks {
		if err := run(); !errors.Is(err, ErrReferencePeriod) {
			t.Errorf("%s in a reference period: err = %v, want ErrReferencePeriod", name, err)
		}
	}

	f.mark(t, false)
	if _, err := f.svc.SyncPayments(ctx, f.club.ID, f.refPeriod, actor); err != nil {
		t.Errorf("sync payments in an ordinary year: %v", err)
	}
}

// reference with dismissed lists the dismissed outgoing rows of reference
// years, and only those can be undismissed: not a final dismissal (duplicate,
// superseded), which would count a cost twice.
func TestUndismissReferenceBankRow(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()
	imp := f.club.NewBankImport(t)
	refOut := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(6, 2), Cents: -20000})
	refIn := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(6, 3), Cents: 20000})
	curOut := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.curDay(6, 2), Cents: -20000})
	refSuperseded := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(6, 4), Cents: -20000})
	refDuplicate := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(6, 5), Cents: -20000})
	reasons := map[string]string{refOut: "internal_transfer", refIn: "internal_transfer", curOut: "internal_transfer",
		refSuperseded: "superseded", refDuplicate: "duplicate"}
	for id, reason := range reasons {
		if err := f.svc.DismissBankRow(ctx, f.club.ID, f.club.Treasurer.ID, id, reason); err != nil {
			t.Fatalf("dismiss %s: %v", reason, err)
		}
	}
	f.mark(t, true)

	list := func(kind string) map[string]BankRowSummary {
		t.Helper()
		rows, err := f.svc.ListUnmatchedBankRows(ctx, f.club.ID, kind, "", 0, 50, 0)
		if err != nil {
			t.Fatalf("list %q: %v", kind, err)
		}
		return rowIDs(rows)
	}
	if got := list("reference"); len(got) != 0 {
		t.Errorf("reference list = %v, want dismissed rows out of it", got)
	}
	got := list("reference,dismissed")
	if len(got) != 1 || !got[refOut].ReferencePeriod || got[refOut].DismissedAt == nil {
		t.Fatalf("reference,dismissed = %v, want only the dismissed outgoing row whose dismissal can be undone", got)
	}
	if got := list("dismissed"); len(got) != 1 || got[curOut].ID == "" {
		t.Errorf("dismissed = %v, want only the current year's", got)
	}

	for name, id := range map[string]string{"incoming": refIn, "current year": curOut, "superseded": refSuperseded, "duplicate": refDuplicate} {
		if _, err := f.svc.UndismissReferenceBankRow(ctx, f.club.ID, id); !errors.Is(err, ErrBankRowNotUndismissable) {
			t.Errorf("undismiss %s row: err = %v, want ErrBankRowNotUndismissable", name, err)
		}
	}
	prior, err := f.svc.UndismissReferenceBankRow(ctx, f.club.ID, refOut)
	if err != nil {
		t.Fatalf("undismiss the reference row: %v", err)
	}
	if prior.Reason != "internal_transfer" || prior.By == nil || *prior.By != f.club.Treasurer.ID || prior.At.IsZero() {
		t.Errorf("prior dismissal = %+v, want the reason, who and when", prior)
	}
	if got := list("reference"); len(got) != 1 || got[refOut].DismissedAt != nil {
		t.Errorf("after undismiss, reference list = %v, want the row back", got)
	}
	if _, err := f.svc.UndismissReferenceBankRow(ctx, f.club.ID, refOut); !errors.Is(err, ErrBankRowNotUndismissable) {
		t.Errorf("undismiss twice: err = %v", err)
	}
}

// Report responses carry the reference flag.
func TestReportsCarryReferenceFlag(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()
	f.createPosted(t, f.refPeriod, f.refYear, twoLines("6200", "1920")...)
	var accountID string
	if err := f.club.Pool.QueryRow(ctx, `SELECT id FROM accounts WHERE club_id = $1 AND code = '6200'`, f.club.ID).Scan(&accountID); err != nil {
		t.Fatalf("account: %v", err)
	}

	flags := func(period string) [5]bool {
		t.Helper()
		is, err := f.svc.IncomeStatement(ctx, f.club.ID, period, false)
		if err != nil {
			t.Fatalf("income statement: %v", err)
		}
		bs, err := f.svc.BalanceSheet(ctx, f.club.ID, period, false)
		if err != nil {
			t.Fatalf("balance sheet: %v", err)
		}
		tb, err := f.svc.TrialBalance(ctx, f.club.ID, period, false)
		if err != nil {
			t.Fatalf("trial balance: %v", err)
		}
		gl, err := f.svc.GeneralLedger(ctx, f.club.ID, period, accountID)
		if err != nil {
			t.Fatalf("general ledger: %v", err)
		}
		mk, err := f.svc.Momskompensasjon(ctx, f.club.ID, period, "simplified")
		if err != nil {
			t.Fatalf("momskomp: %v", err)
		}
		return [5]bool{is.ReferenceOnly, bs.ReferenceOnly, tb.ReferenceOnly, gl.ReferenceOnly, mk.ReferenceOnly}
	}
	if got := flags(f.refPeriod); got != [5]bool{} {
		t.Errorf("unmarked flags = %v, want none", got)
	}
	f.mark(t, true)
	if got := flags(f.refPeriod); got != [5]bool{true, true, true, true, true} {
		t.Errorf("reference flags = %v, want all set", got)
	}
	if got := flags(f.club.PeriodID); got != [5]bool{} {
		t.Errorf("current year flags = %v, want none", got)
	}
}

// A 3900 charge booked in a reference year is neither listed nor reclassified
// by the 3900 reclassification, in preview or apply, and the transaction
// re-checks the year under its lock.
func TestFallbackReclassLeavesReferenceYear(t *testing.T) {
	f := newFallbackFixture(t)
	ctx := context.Background()
	refYear := f.club.Year - 1
	period := addPeriod(t, f.club, refYear)
	f.priceItem(t, "guest", "Gjesteplass synthetic", 80000, "day", "all")
	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 80000, Date: time.Date(refYear, 6, 1, 0, 0, 0, 0, time.UTC), CustomerName: "Reference Payer"})
	f.parkRow(t, row)

	plan := f.preview(t)
	it := f.item(t, plan, row)
	c := &fallbackCharge{entryID: it.EntryID, rowID: row}
	c.entryNumber = it.EntryNumber

	if _, _, err := f.svc.SetPeriodReference(ctx, f.club.ID, period, true); err != nil {
		t.Fatalf("mark: %v", err)
	}
	entries := f.entryCount(t)

	if _, err := f.svc.PreviewVippsFallbackReclass(ctx, f.club.ID, period); !errors.Is(err, ErrReferencePeriod) {
		t.Errorf("preview of a reference period = %v, want ErrReferencePeriod", err)
	}
	if _, err := f.svc.ApplyVippsFallbackReclass(ctx, f.club.ID, f.club.Treasurer.ID, period, map[string]string{it.EntryID: "3200"}); !errors.Is(err, ErrReferencePeriod) {
		t.Errorf("apply on a reference period = %v, want ErrReferencePeriod", err)
	}
	res := f.preview(t)
	if len(res.Items) != 0 || res.ClosedPeriodCharges != 0 {
		t.Errorf("preview = %+v, want the reference year's charge neither listed nor counted", res)
	}
	if applied := f.apply(t, map[string]string{it.EntryID: "3200"}); applied.Reclassified != 0 || len(applied.StaleIDs) != 1 {
		t.Errorf("apply = %+v, want the stale target ignored", applied)
	}
	if f.entryCount(t) != entries || f.rowEntry(t, row) != it.EntryID {
		t.Fatalf("a reference year was changed")
	}

	lines, err := entryLineTotals(ctx, f.club.Pool, it.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	c.lines, c.amount = lines, 800
	if _, err = f.svc.reclassifyFallbackCharge(ctx, f.club.ID, f.club.Treasurer.ID, vippsClearingAccountCode, c, "3200", "x"); !errors.Is(err, ErrReferencePeriod) {
		t.Errorf("reclassify in a reference year = %v, want ErrReferencePeriod", err)
	}
	if f.entryCount(t) != entries || f.entryStatus(t, it.EntryID) != "posted" {
		t.Errorf("a refused reclass left a partial change")
	}

	// A year marked after the plan was made is reported with its own reason code.
	if _, _, err := f.svc.SetPeriodReference(ctx, f.club.ID, period, false); err != nil {
		t.Fatalf("unmark: %v", err)
	}
	_, stale, err := f.svc.planVippsFallbackReclass(ctx, f.club.ID, "")
	if err != nil || len(stale.items) != 1 {
		t.Fatalf("plan = %+v (%v), want the charge again", stale, err)
	}
	if _, _, err := f.svc.SetPeriodReference(ctx, f.club.ID, period, true); err != nil {
		t.Fatalf("mark: %v", err)
	}
	item := &stale.items[0]
	f.svc.applyFallbackReclassOne(ctx, f.club.ID, f.club.Treasurer.ID, stale, item)
	if item.Outcome != FallbackOutcomeFailed || item.ReasonCode != reasonCodeReferencePeriod {
		t.Errorf("item = %+v, want a failure with the reference_period code", item)
	}
}

// pdfText inflates a PDF's content streams so a test can read what was drawn.
func pdfText(pdf []byte) string {
	var out strings.Builder
	rest := pdf
	for {
		start := bytes.Index(rest, []byte("stream\n"))
		if start < 0 {
			return out.String()
		}
		rest = rest[start+len("stream\n"):]
		end := bytes.Index(rest, []byte("endstream"))
		if end < 0 {
			return out.String()
		}
		if r, err := zlib.NewReader(bytes.NewReader(rest[:end])); err == nil {
			b, _ := io.ReadAll(r)
			out.Write(b)
		} else {
			out.Write(rest[:end])
		}
		rest = rest[end:]
	}
}

// The report PDFs draw Norwegian letters as themselves (WinAnsi), not as the
// bytes of their UTF-8 encoding, and a reference year says so.
func TestReportPDFsDrawNorwegianLettersAndReferenceLine(t *testing.T) {
	header := ReportHeader{ClubName: "Båtlaget Ærlig", OrgNumber: "999999999", Year: 2025}
	is, err := IncomeStatementPDF(header, &IncomeStatement{ReferenceOnly: true,
		Expenses: []ReportLine{{AccountCode: "6200", AccountName: "Vedlikehold bryggeanlegg Ø", Amount: 100}}})
	if err != nil {
		t.Fatalf("income statement pdf: %v", err)
	}
	bs, err := BalanceSheetPDF(header, &BalanceSheet{ReferenceOnly: true, IsBalanced: true})
	if err != nil {
		t.Fatalf("balance sheet pdf: %v", err)
	}
	for name, pdf := range map[string][]byte{"resultat": is, "balanse": bs} {
		text := pdfText(pdf)
		if !strings.Contains(text, "Referanse\xe5r") || strings.Contains(text, "\xc3\xa5") {
			t.Errorf("%s pdf: want the reference line in WinAnsi, not UTF-8 bytes", name)
		}
		if !strings.Contains(text, "B\xe5tlaget \xc6rlig") {
			t.Errorf("%s pdf: the club name is not drawn in WinAnsi", name)
		}
	}
	if !strings.Contains(pdfText(is), "Beløp") && !strings.Contains(pdfText(is), "Bel\xf8p") {
		t.Errorf("income statement pdf: Beløp missing")
	}
	plain, err := IncomeStatementPDF(header, &IncomeStatement{})
	if err != nil {
		t.Fatalf("pdf: %v", err)
	}
	if strings.Contains(pdfText(plain), "Referanse") {
		t.Errorf("an ordinary year's pdf carries the reference line")
	}
}

// A line's mva_amount is never negative: with momskomp signing it by side, a
// negative value on a credit line would add instead of subtract.
func TestCreateJournalEntryRefusesNegativeMVA(t *testing.T) {
	f := newRefFixture(t, false)
	_, err := f.svc.CreateJournalEntry(context.Background(), CreateJournalEntryInput{
		FiscalPeriodID: f.club.PeriodID, EntryDate: dateIn(f.club.Year, 5, 2), Description: "Synthetic bilag",
		CreatedBy: f.club.Treasurer.ID, ClubID: f.club.ID,
		Lines: []CreateJournalLineInput{
			{AccountCode: "6200", Debit: 100, MVAAmount: -20}, {AccountCode: "1920", Credit: 100},
		},
	})
	if !errors.Is(err, ErrNegativeMVA) {
		t.Fatalf("err = %v, want ErrNegativeMVA", err)
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID); n != 0 {
		t.Errorf("a refused entry wrote %d rows", n)
	}
}
