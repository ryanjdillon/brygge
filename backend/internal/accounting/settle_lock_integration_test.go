package accounting

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// settleLockFixture is a club for the Vipps paths that settle fakturas
// outside a single-faktura assign: the legacy settlement reconcile confirm and
// an order-linked charge. They re-check each faktura under its lock, as a
// manual assign does (lockManual), rather than trust a preview.
type settleLockFixture struct {
	*importedFixture
}

func newSettleLockFixture(t *testing.T) *settleLockFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t, testutil.WithMaxConns(3))
	svc := NewService(pool, audit.NewService(pool, zerolog.Nop()), zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	return &settleLockFixture{&importedFixture{svc: svc, club: club, member: club.NewMember(t, "member")}}
}

func (f *settleLockFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.club.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func (f *settleLockFixture) entries(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *settleLockFixture) vippsBooked(t *testing.T, rowID string) bool {
	t.Helper()
	var booked bool
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT journal_entry_id IS NOT NULL FROM vipps_import_rows WHERE id = $1`, rowID).Scan(&booked); err != nil {
		t.Fatal(err)
	}
	return booked
}

// confirmLines is the settlement preview for one 450 kr faktura paid by one
// charge.
func confirmLines(inv fin.Invoice, chargeID string) []VippsReconcileLine {
	return []VippsReconcileLine{
		{Kind: "bank_in", AccountCode: "1920", Debit: 450},
		{Kind: "receivable", AccountCode: receivablesAccountCode, Credit: 450, LinkedInvoiceID: inv.ID, VippsRowID: chargeID},
	}
}

// A faktura that was credited, voided, paid or booked after the preview or
// the order was built is refused by the legacy Vipps confirm and by an
// order-linked charge, with nothing booked: crediting 1500 for it again would
// leave a credit nothing offsets.
func TestVippsSettlePathsRefuseFakturaChangedSinceLookup(t *testing.T) {
	f := newSettleLockFixture(t)
	ctx := context.Background()
	c := f.club
	imp := c.NewBankImport(t)

	cases := []struct {
		name string
		make func() fin.Invoice
		want error
	}{
		{"credited", func() fin.Invoice {
			inv := c.NewInvoice(t, fin.InvoiceOpts{Member: &f.member, Cents: 45000})
			m := fin.Member{ID: inv.MemberID}
			cn := c.NewInvoice(t, fin.InvoiceOpts{Member: &m, Lines: []fin.Line{{Category: "slip_fee", Cents: -45000}}})
			f.exec(t, `UPDATE invoices SET credits_invoice_id = $1 WHERE id = $2`, inv.ID, cn.ID)
			return inv
		}, ErrInvoiceCredited},
		{"voided", func() fin.Invoice {
			return c.NewInvoice(t, fin.InvoiceOpts{Member: &f.member, Cents: 45000, Status: "voided"})
		}, ErrInvoiceVoided},
		{"paid", func() fin.Invoice {
			return c.NewInvoice(t, fin.InvoiceOpts{Member: &f.member, Cents: 45000, Paid: true})
		}, errInvoicePaid},
		{"booked bank row", func() fin.Invoice {
			inv := c.NewInvoice(t, fin.InvoiceOpts{Member: &f.member, Cents: 45000})
			if _, err := f.svc.AssignBankRowToInvoice(ctx, c.ID, c.Treasurer.ID, c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000}), inv.ID); err != nil {
				t.Fatalf("book the faktura: %v", err)
			}
			// The window before the payment back-link lands.
			f.exec(t, `UPDATE invoices SET payment_id = NULL WHERE id = $1`, inv.ID)
			return inv
		}, errInvoiceHasBookedRow},
		{"booked Vipps charge", func() fin.Invoice {
			inv := c.NewInvoice(t, fin.InvoiceOpts{Member: &f.member, Cents: 45000})
			charge := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payer"})
			if _, err := f.svc.AssignVippsChargeToInvoice(ctx, c.ID, charge, inv.ID, c.Treasurer.ID); err != nil {
				t.Fatalf("book the faktura: %v", err)
			}
			f.exec(t, `UPDATE invoices SET payment_id = NULL WHERE id = $1`, inv.ID)
			return inv
		}, errInvoiceHasBookedRow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv := tc.make()
			before := f.entries(t)

			bankRow := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000})
			confirmCharge := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payer"})
			if _, err := f.svc.ReconcileVippsConfirm(ctx, c.ID, bankRow, "", c.Treasurer.ID, confirmLines(inv, confirmCharge)); !errors.Is(err, tc.want) {
				t.Errorf("confirm err = %v, want %v", err, tc.want)
			}
			if f.bankRowBooked(t, bankRow) || f.vippsBooked(t, confirmCharge) {
				t.Errorf("refused confirm booked the bank row or the charge")
			}

			order := c.NewOrderForInvoice(t, inv)
			orderCharge := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, OrderID: order.VippsReference})
			_, found, err := f.svc.AssignVippsChargeToOrder(ctx, c.ID, orderCharge, c.Treasurer.ID)
			if !found || !errors.Is(err, tc.want) {
				t.Errorf("order charge: found %v, err %v; want found and %v", found, err, tc.want)
			}
			if err != nil && !strings.Contains(err.Error(), "book the charge by hand") {
				t.Errorf("order charge refusal reads %q, want the skipped_details wording", err.Error())
			}
			if f.vippsBooked(t, orderCharge) {
				t.Errorf("refused order charge was booked")
			}
			if n := f.entries(t); n != before {
				t.Errorf("%d journal entries after the refusals, want %d", n, before)
			}
		})
	}
}

// The confirm records the faktura on each charge it books, so a bank or Vipps
// assign that runs next sees it as booked even before the payment back-link
// lands (the same window an order charge leaves).
func TestReconcileVippsConfirmRecordsSettledFaktura(t *testing.T) {
	f := newSettleLockFixture(t)
	ctx := context.Background()
	c := f.club
	imp := c.NewBankImport(t)
	inv := c.NewInvoice(t, fin.InvoiceOpts{Member: &f.member, Cents: 45000})
	bankRow := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000})
	charge := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payer"})

	entryID, err := f.svc.ReconcileVippsConfirm(ctx, c.ID, bankRow, "", c.Treasurer.ID, confirmLines(inv, charge))
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	var recorded *string
	var entryStatus string
	if err := c.Pool.QueryRow(ctx,
		`SELECT r.invoice_id::text, e.status FROM vipps_import_rows r JOIN journal_entries e ON e.id = r.journal_entry_id
		  WHERE r.id = $1 AND e.id = $2`, charge, entryID).Scan(&recorded, &entryStatus); err != nil {
		t.Fatalf("charge: %v", err)
	}
	if recorded == nil || *recorded != inv.ID || entryStatus != "posted" {
		t.Fatalf("charge records faktura %v, entry %s; want %s, posted", recorded, entryStatus, inv.ID)
	}
	if !f.paid(t, inv) {
		t.Errorf("confirm did not link the faktura's payment")
	}

	f.exec(t, `UPDATE invoices SET payment_id = NULL WHERE id = $1`, inv.ID)
	again := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000})
	if _, err := f.svc.AssignBankRowToInvoice(ctx, c.ID, c.Treasurer.ID, again, inv.ID); !errors.Is(err, errInvoiceHasBookedRow) {
		t.Errorf("second assign err = %v, want errInvoiceHasBookedRow", err)
	}
}

// waitForLockWaiter blocks until a backend is blocked by holderPID, so the
// test changes the faktura only after the booking under test is queued
// behind the holder's lock.
func waitForLockWaiter(t *testing.T, f *settleLockFixture, holderPID int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := f.club.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid))`, holderPID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the booking never queued behind the faktura lock")
}

// A faktura voided while the booking waits for its lock is refused: the
// check runs after the lock, on what the lock holder committed, not on what
// the caller read first.
func TestVippsSettlePathsSeeChangeCommittedWhileWaiting(t *testing.T) {
	f := newSettleLockFixture(t)
	c := f.club
	imp := c.NewBankImport(t)

	type booking struct {
		book   func() error
		booked func() bool
	}
	paths := map[string]func(t *testing.T, inv fin.Invoice) booking{
		"confirm": func(t *testing.T, inv fin.Invoice) booking {
			row := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000})
			charge := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payer"})
			return booking{
				book: func() error {
					_, err := f.svc.ReconcileVippsConfirm(context.Background(), c.ID, row, "", c.Treasurer.ID, confirmLines(inv, charge))
					return err
				},
				booked: func() bool { return f.bankRowBooked(t, row) || f.vippsBooked(t, charge) },
			}
		},
		"order charge": func(t *testing.T, inv fin.Invoice) booking {
			order := c.NewOrderForInvoice(t, inv)
			charge := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, OrderID: order.VippsReference})
			return booking{
				book: func() error {
					_, _, err := f.svc.AssignVippsChargeToOrder(context.Background(), c.ID, charge, c.Treasurer.ID)
					return err
				},
				booked: func() bool { return f.vippsBooked(t, charge) },
			}
		},
	}
	for name, setup := range paths {
		t.Run(name, func(t *testing.T) {
			inv := c.NewInvoice(t, fin.InvoiceOpts{Member: &f.member, Cents: 45000})
			b := setup(t, inv)
			before := f.entries(t)

			ctx := context.Background()
			holder, err := c.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = holder.Rollback(ctx) }()
			var holderPID int
			if err := holder.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
				t.Fatal(err)
			}
			if _, err := holder.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1 FOR NO KEY UPDATE`, inv.ID); err != nil {
				t.Fatal(err)
			}

			done := make(chan error, 1)
			go func() { done <- b.book() }()
			waitForLockWaiter(t, f, holderPID)
			if _, err := holder.Exec(ctx, `UPDATE invoices SET status = 'voided' WHERE id = $1`, inv.ID); err != nil {
				t.Fatal(err)
			}
			if err := holder.Commit(ctx); err != nil {
				t.Fatal(err)
			}

			select {
			case err := <-done:
				if !errors.Is(err, ErrInvoiceVoided) {
					t.Errorf("err = %v, want ErrInvoiceVoided", err)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("booking never finished after the lock was released")
			}
			if b.booked() || f.entries(t) != before {
				t.Errorf("a booking that lost to the void was committed")
			}
		})
	}
}

func (f *settleLockFixture) charge(t *testing.T, rowID string) (entryID, invoiceID *string) {
	t.Helper()
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT journal_entry_id::text, invoice_id::text FROM vipps_import_rows WHERE id = $1`, rowID).Scan(&entryID, &invoiceID); err != nil {
		t.Fatal(err)
	}
	return entryID, invoiceID
}

// Undoing a legacy settlement (Angre on its bank row), posted or still a
// draft (the resync's), releases the bank row, every Vipps row it booked and
// every faktura those charges recorded, so the fakturas can be settled again
// by a confirm or by a manual assign.
func TestUnassignLegacySettlementReopensEveryFaktura(t *testing.T) {
	for _, post := range []bool{true, false} {
		name := "posted"
		if !post {
			name = "draft"
		}
		t.Run(name, func(t *testing.T) {
			f := newSettleLockFixture(t)
			ctx := context.Background()
			c := f.club
			imp := c.NewBankImport(t)
			invA := c.NewInvoice(t, fin.InvoiceOpts{Member: &f.member, Cents: 45000})
			invB := c.NewInvoice(t, fin.InvoiceOpts{Member: &f.member, Cents: 30000})
			chargeA := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payer"})
			chargeB := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, CustomerName: "Synthetic Payer"})
			lines := []VippsReconcileLine{
				{Kind: "bank_in", AccountCode: "1920", Debit: 750},
				{Kind: "receivable", AccountCode: receivablesAccountCode, Credit: 450, LinkedInvoiceID: invA.ID, VippsRowID: chargeA},
				{Kind: "receivable", AccountCode: receivablesAccountCode, Credit: 300, LinkedInvoiceID: invB.ID, VippsRowID: chargeB},
			}
			confirm := func(row string) {
				t.Helper()
				if _, err := f.svc.reconcileVippsConfirm(ctx, c.ID, row, "", c.Treasurer.ID, lines, post); err != nil {
					t.Fatalf("confirm: %v", err)
				}
			}

			bankRow := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 75000})
			confirm(bankRow)
			if !f.paid(t, invA) || !f.paid(t, invB) {
				t.Fatalf("confirm did not pay both fakturas")
			}

			res, err := f.svc.UnassignBankRowReported(ctx, c.ID, c.Treasurer.ID, bankRow)
			if err != nil {
				t.Fatalf("unassign: %v", err)
			}
			for label, charge := range map[string]string{"A": chargeA, "B": chargeB} {
				if entry, inv := f.charge(t, charge); entry != nil || inv != nil {
					t.Errorf("charge %s after unassign: entry %v, faktura %v; want both cleared", label, entry, inv)
				}
			}
			if f.paid(t, invA) || f.paid(t, invB) {
				t.Errorf("unassign left a faktura paid: A %v, B %v", f.paid(t, invA), f.paid(t, invB))
			}
			wantInvoices := []string{invA.ID, invB.ID}
			slices.Sort(wantInvoices)
			wantCharges := []string{chargeA, chargeB}
			slices.Sort(wantCharges)
			if !slices.Equal(res.ReopenedInvoiceIDs, wantInvoices) || !slices.Equal(res.ReleasedVippsRowIDs, wantCharges) {
				t.Errorf("result reopened %v, released %v; want %v, %v",
					res.ReopenedInvoiceIDs, res.ReleasedVippsRowIDs, wantInvoices, wantCharges)
			}

			// The same settlement books again.
			confirm(bankRow)
			if _, _, err := f.svc.UnassignBankRow(ctx, c.ID, c.Treasurer.ID, bankRow); err != nil {
				t.Fatalf("second unassign: %v", err)
			}
			// And a manual assign of a released faktura is not blocked.
			other := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000})
			if _, err := f.svc.AssignBankRowToInvoice(ctx, c.ID, c.Treasurer.ID, other, invA.ID); err != nil {
				t.Errorf("manual assign after unassign: %v", err)
			}
		})
	}
}

// Undoing a settlement touches only what the settlement booked. A settlement
// with a charge booked to 1500 unlinked, and another member's faktura paid by
// a manual bank assign the same day for the same amount, leaves that
// faktura, its payment and its entry alone.
func TestUnassignLegacySettlementLeavesSameDayBookingAlone(t *testing.T) {
	f := newSettleLockFixture(t)
	ctx := context.Background()
	c := f.club
	imp := c.NewBankImport(t)
	day := time.Date(c.Year, 6, 10, 0, 0, 0, 0, time.UTC)

	other := fin.Member{ID: c.NewMember(t, "member").ID}
	otherInv := c.NewInvoice(t, fin.InvoiceOpts{Member: &other, Cents: 45000})
	otherRow := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000, Date: day})
	otherEntry, err := f.svc.AssignBankRowToInvoice(ctx, c.ID, c.Treasurer.ID, otherRow, otherInv.ID)
	if err != nil {
		t.Fatalf("book the other faktura: %v", err)
	}
	if !f.paid(t, otherInv) {
		t.Fatal("the other faktura is not paid")
	}

	charge := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payer"})
	settlementRow := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000, Date: day})
	if _, err := f.svc.ReconcileVippsConfirm(ctx, c.ID, settlementRow, "", c.Treasurer.ID, []VippsReconcileLine{
		{AccountCode: "1920", Debit: 450},
		{AccountCode: receivablesAccountCode, Credit: 450, VippsRowID: charge},
	}); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	if _, _, err := f.svc.UnassignBankRow(ctx, c.ID, c.Treasurer.ID, settlementRow); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if !f.paid(t, otherInv) {
		t.Errorf("undoing the settlement removed another faktura's payment")
	}
	var status string
	if err := c.Pool.QueryRow(ctx,
		`SELECT status FROM journal_entries WHERE id = $1`, otherEntry).Scan(&status); err != nil || status != "posted" {
		t.Errorf("the other booking's entry is %q (%v), want posted", status, err)
	}
	if !f.bankRowBooked(t, otherRow) {
		t.Errorf("the other booking's bank row was released")
	}
	if f.bankRowBooked(t, settlementRow) || f.vippsBooked(t, charge) {
		t.Errorf("the settlement was not released")
	}
}

// A settlement whose Vipps row was booked another way meanwhile (per charge)
// is refused with nothing booked, rather than booking the charge twice.
func TestReconcileVippsConfirmRefusesRowAlreadyBooked(t *testing.T) {
	f := newSettleLockFixture(t)
	ctx := context.Background()
	c := f.club
	imp := c.NewBankImport(t)
	charge := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payer"})
	if _, err := f.svc.CreateVippsChargeBilag(ctx, c.ID, charge, "3900", "", c.Treasurer.ID); err != nil {
		t.Fatalf("book the charge: %v", err)
	}
	before := f.entries(t)

	bankRow := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000})
	_, err := f.svc.ReconcileVippsConfirm(ctx, c.ID, bankRow, "", c.Treasurer.ID, []VippsReconcileLine{
		{AccountCode: "1920", Debit: 450},
		{AccountCode: receivablesAccountCode, Credit: 450, VippsRowID: charge},
	})
	if !errors.Is(err, ErrVippsRowAlreadyBooked) {
		t.Fatalf("confirm err = %v, want ErrVippsRowAlreadyBooked", err)
	}
	if f.bankRowBooked(t, bankRow) || f.entries(t) != before {
		t.Errorf("a refused confirm booked the bank row or left an entry")
	}
}

// A single charge of a settlement is not undone on its own: that would void
// the bilag the bank row and the other charges share.
func TestUnassignVippsChargeRefusesLegacySettlementCharge(t *testing.T) {
	f := newSettleLockFixture(t)
	ctx := context.Background()
	c := f.club
	inv := c.NewInvoice(t, fin.InvoiceOpts{Member: &f.member, Cents: 45000})
	charge := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payer"})
	bankRow := c.NewBankRow(t, c.NewBankImport(t), fin.BankRowOpts{Cents: 45000})
	if _, err := f.svc.ReconcileVippsConfirm(ctx, c.ID, bankRow, "", c.Treasurer.ID, confirmLines(inv, charge)); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if _, _, err := f.svc.UnassignVippsCharge(ctx, c.ID, c.Treasurer.ID, charge); err == nil {
		t.Fatal("unassigning one charge of a settlement succeeded")
	}
	if !f.bankRowBooked(t, bankRow) || !f.vippsBooked(t, charge) {
		t.Errorf("the refused unassign changed the settlement")
	}
}

// A confirm links a faktura from one line only and credits it with no more
// than its total (within the preview's 1 kr tolerance); lines are
// client-editable, so a line that links a faktura must also name its charge.
func TestReconcileVippsConfirmRefusesOverpaidFaktura(t *testing.T) {
	f := newSettleLockFixture(t)
	ctx := context.Background()
	c := f.club
	imp := c.NewBankImport(t)
	inv := c.NewInvoice(t, fin.InvoiceOpts{Member: &f.member, Cents: 45000})
	chargeOne := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payer"})
	chargeTwo := c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payer"})
	before := f.entries(t)

	cases := []struct {
		name  string
		lines []VippsReconcileLine
		want  error
	}{
		{"one faktura on two lines", []VippsReconcileLine{
			{AccountCode: "1920", Debit: 900},
			{AccountCode: receivablesAccountCode, Credit: 450, LinkedInvoiceID: inv.ID, VippsRowID: chargeOne},
			{AccountCode: receivablesAccountCode, Credit: 450, LinkedInvoiceID: inv.ID, VippsRowID: chargeTwo},
		}, ErrSettlementInvoiceOverpaid},
		{"credit above the total", []VippsReconcileLine{
			{AccountCode: "1920", Debit: 452},
			{AccountCode: receivablesAccountCode, Credit: 452, LinkedInvoiceID: inv.ID, VippsRowID: chargeOne},
		}, ErrSettlementInvoiceOverpaid},
		{"link without a charge", []VippsReconcileLine{
			{AccountCode: "1920", Debit: 450},
			{AccountCode: receivablesAccountCode, Credit: 450, LinkedInvoiceID: inv.ID},
		}, nil},
	}
	for _, tc := range cases {
		bankRow := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000})
		_, err := f.svc.ReconcileVippsConfirm(ctx, c.ID, bankRow, "", c.Treasurer.ID, tc.lines)
		if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		if f.bankRowBooked(t, bankRow) || f.vippsBooked(t, chargeOne) || f.vippsBooked(t, chargeTwo) || f.paid(t, inv) {
			t.Errorf("%s: a refused confirm booked something", tc.name)
		}
	}
	if n := f.entries(t); n != before {
		t.Errorf("%d journal entries after the refusals, want %d", n, before)
	}

	// Within the tolerance a charge a krone over still books.
	row := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45050})
	if _, err := f.svc.ReconcileVippsConfirm(ctx, c.ID, row, "", c.Treasurer.ID, []VippsReconcileLine{
		{AccountCode: "1920", Debit: 450.5},
		{AccountCode: receivablesAccountCode, Credit: 450.5, LinkedInvoiceID: inv.ID, VippsRowID: chargeOne},
	}); err != nil {
		t.Errorf("charge within the tolerance: %v", err)
	}
}

// The preview offers a faktura to the first charge that matches it, and not
// at all when a booked row is already recorded against it: both would be
// refused by the confirm's lock, which must not take the rest of the
// settlement down with them.
func TestReconcileVippsPreviewLinksEachFakturaOnce(t *testing.T) {
	f := newSettleLockFixture(t)
	ctx := context.Background()
	c := f.club
	imp := c.NewBankImport(t)
	f.exec(t, `UPDATE users SET first_name = 'Synthetic', last_name = 'Payer' WHERE id = $1`, f.member.ID)

	// A settlement of two equal charges from one member.
	settlement := func(desc string, charges int, date time.Time) (string, []string) {
		var ids []string
		for range charges {
			ids = append(ids, c.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payer", MSN: "000001", Date: date}))
		}
		c.NewVippsRow(t, fin.VippsRowOpts{RowType: "payout", Cents: -int64(charges) * 45000, Settlement: desc, MSN: "000001", Date: date})
		return c.NewBankRow(t, imp, fin.BankRowOpts{Cents: int64(charges) * 45000, Description: "Utb. " + desc + " Vippsnr 000001"}), ids
	}

	inv := c.NewInvoice(t, fin.InvoiceOpts{Member: &f.member, Cents: 45000, IssueDate: time.Now().AddDate(0, 0, -5)})
	bankRow, charges := settlement("2000101", 2, time.Date(c.Year, 6, 2, 0, 0, 0, 0, time.UTC))
	preview, err := f.svc.ReconcileVippsPreview(ctx, c.ID, bankRow)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	linked := 0
	for _, l := range preview.Lines {
		if l.LinkedInvoiceID == inv.ID {
			linked++
		}
	}
	if linked != 1 {
		t.Fatalf("%d charges link the one faktura, want 1: %+v", linked, preview.Lines)
	}
	if _, err := f.svc.ReconcileVippsConfirm(ctx, c.ID, bankRow, "", c.Treasurer.ID, preview.Lines); err != nil {
		t.Fatalf("confirming the preview: %v", err)
	}
	for _, id := range charges {
		if !f.vippsBooked(t, id) {
			t.Errorf("charge %s was not booked", id)
		}
	}

	// A faktura a booked row is recorded against is not offered; the charge
	// falls through to an unlinked receivable and the settlement still books.
	booked := c.NewInvoice(t, fin.InvoiceOpts{Member: &f.member, Cents: 45000, IssueDate: time.Now().AddDate(0, 0, -4)})
	if _, err := f.svc.AssignBankRowToInvoice(ctx, c.ID, c.Treasurer.ID, c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000}), booked.ID); err != nil {
		t.Fatalf("book the faktura: %v", err)
	}
	f.exec(t, `UPDATE invoices SET payment_id = NULL WHERE id = $1`, booked.ID)
	bankRow, charges = settlement("2000102", 1, time.Date(c.Year, 6, 9, 0, 0, 0, 0, time.UTC))
	preview, err = f.svc.ReconcileVippsPreview(ctx, c.ID, bankRow)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	for _, l := range preview.Lines {
		if l.LinkedInvoiceID != "" {
			t.Errorf("preview linked %s to a faktura with a booked row", l.LinkedInvoiceID)
		}
	}
	if _, err := f.svc.ReconcileVippsConfirm(ctx, c.ID, bankRow, "", c.Treasurer.ID, preview.Lines); err != nil {
		t.Fatalf("confirming the preview: %v", err)
	}
	if !f.vippsBooked(t, charges[0]) {
		t.Errorf("the unlinked charge was not booked")
	}
}
