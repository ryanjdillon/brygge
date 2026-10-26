package accounting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// importedFixture is a club with one member and helpers to seed imported
// (previous-system) fakturas and opening balances (BRY-386).
type importedFixture struct {
	svc    *Service
	club   *fin.Club
	member fin.Member
}

func newImportedFixture(t *testing.T) *importedFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, audit.NewService(pool, zerolog.Nop()), zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	return &importedFixture{svc: svc, club: club, member: club.NewMember(t, "member")}
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func (f *importedFixture) imported(t *testing.T, issued time.Time, cents int64) fin.Invoice {
	t.Helper()
	return f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.member, Cents: cents, Imported: true, IssueDate: issued})
}

// openingBalance posts the cutover entry carrying a receivable over: DR 1500
// / CR equity, dated on.
func (f *importedFixture) openingBalance(t *testing.T, on time.Time, cents int64) {
	t.Helper()
	ctx := context.Background()
	ob, err := f.svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: f.club.PeriodID,
		EntryDate:      on.Format("2006-01-02"),
		Description:    "Inngående balanse",
		Source:         "opening_balance",
		CreatedBy:      f.club.Treasurer.ID,
		ClubID:         f.club.ID,
		Lines: []CreateJournalLineInput{
			{AccountCode: receivablesAccountCode, Debit: fin.Kr(cents)},
			{AccountCode: "2050", Credit: fin.Kr(cents)},
		},
	})
	if err != nil {
		t.Fatalf("opening balance: %v", err)
	}
	if err := f.svc.PostJournalEntry(ctx, f.club.ID, ob.ID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("post opening balance: %v", err)
	}
}

func (f *importedFixture) bankRowBooked(t *testing.T, rowID string) bool {
	t.Helper()
	var booked bool
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT journal_entry_id IS NOT NULL FROM bank_import_rows WHERE id = $1`, rowID).Scan(&booked); err != nil {
		t.Fatalf("bank row: %v", err)
	}
	return booked
}

func (f *importedFixture) paid(t *testing.T, inv fin.Invoice) bool {
	t.Helper()
	var paid bool
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT payment_id IS NOT NULL FROM invoices WHERE id = $1`, inv.ID).Scan(&paid); err != nil {
		t.Fatalf("invoice: %v", err)
	}
	return paid
}

// A manual Tildel of a bank row against an imported faktura credits 1500,
// so it is booked only when an opening balance carrying the faktura's
// receivable is posted on or after its issue date and on or before the row.
func TestAssignBankRowToImportedFaktura(t *testing.T) {
	cases := []struct {
		name    string
		issued  time.Time
		ob      time.Time // zero: no opening balance
		row     time.Time
		booking bool
	}{
		{"no opening balance", day(2025, 9, 1), time.Time{}, day(2026, 6, 1), false},
		{"opening balance between faktura and row", day(2025, 9, 1), day(2026, 1, 1), day(2026, 6, 1), true},
		{"opening balance on the row date", day(2025, 9, 1), day(2026, 6, 1), day(2026, 6, 1), true},
		{"opening balance after the row", day(2025, 9, 1), day(2026, 7, 1), day(2026, 6, 1), false},
		{"opening balance before the faktura", day(2026, 3, 1), day(2026, 1, 1), day(2026, 6, 1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newImportedFixture(t)
			inv := f.imported(t, tc.issued, 90000)
			if !tc.ob.IsZero() {
				f.openingBalance(t, tc.ob, 90000)
			}
			rowID := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Date: tc.row, Cents: 90000})

			_, err := f.svc.AssignBankRowToInvoice(context.Background(), f.club.ID, f.club.Treasurer.ID, rowID, inv.ID)
			if tc.booking {
				if err != nil {
					t.Fatalf("assign: %v", err)
				}
				if !f.bankRowBooked(t, rowID) || !f.paid(t, inv) {
					t.Errorf("row booked %v, faktura paid %v; want both", f.bankRowBooked(t, rowID), f.paid(t, inv))
				}
				return
			}
			if !errors.Is(err, ErrImportedNoOpeningBalance) {
				t.Fatalf("assign err = %v, want ErrImportedNoOpeningBalance", err)
			}
			if f.bankRowBooked(t, rowID) || f.paid(t, inv) {
				t.Errorf("refused assign left row booked %v, faktura paid %v", f.bankRowBooked(t, rowID), f.paid(t, inv))
			}
		})
	}
}

// An imported faktura issued after the cutover isn't in the opening balance;
// once it has its own posted receivable booking (invoice_sync), its payment
// may credit 1500 like any Brygge faktura's.
func TestAssignBankRowToImportedFakturaWithOwnBooking(t *testing.T) {
	f := newImportedFixture(t)
	ctx := context.Background()
	inv := f.imported(t, day(2026, 2, 22), 45000)
	sourceTable := "invoices"
	entry, err := f.svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: f.club.PeriodID,
		EntryDate:      "2026-02-22",
		Description:    "Faktura",
		Source:         "invoice_sync",
		SourceID:       &inv.ID,
		SourceTable:    &sourceTable,
		CreatedBy:      f.club.Treasurer.ID,
		ClubID:         f.club.ID,
		Lines: []CreateJournalLineInput{
			{AccountCode: receivablesAccountCode, Debit: fin.Kr(45000)},
			{AccountCode: "3100", Credit: fin.Kr(45000)},
		},
	})
	if err != nil {
		t.Fatalf("invoice booking: %v", err)
	}
	rowID := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Date: day(2026, 6, 1), Cents: 45000})

	// A draft booking is no receivable yet.
	if _, err := f.svc.AssignBankRowToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, rowID, inv.ID); !errors.Is(err, ErrImportedNoOpeningBalance) {
		t.Fatalf("assign against a draft booking: err = %v, want ErrImportedNoOpeningBalance", err)
	}
	if err := f.svc.PostJournalEntry(ctx, f.club.ID, entry.ID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("post invoice booking: %v", err)
	}
	if _, err := f.svc.AssignBankRowToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, rowID, inv.ID); err != nil {
		t.Fatalf("assign against the posted booking: %v", err)
	}
	if !f.bankRowBooked(t, rowID) || !f.paid(t, inv) {
		t.Errorf("row booked %v, faktura paid %v; want both", f.bankRowBooked(t, rowID), f.paid(t, inv))
	}
}

// Each row of a multi-row assign is booked on its own date, so every row
// must find the receivable already in the GL: the earliest row decides.
func TestAssignMultipleBankRowsToImportedFakturaUsesEarliestRow(t *testing.T) {
	f := newImportedFixture(t)
	ctx := context.Background()
	inv := f.imported(t, day(2025, 9, 1), 90000)
	f.openingBalance(t, day(2026, 6, 15), 90000)
	importID := f.club.NewBankImport(t)
	early := f.club.NewBankRow(t, importID, fin.BankRowOpts{Date: day(2026, 6, 10), Cents: 40000})
	late := f.club.NewBankRow(t, importID, fin.BankRowOpts{Date: day(2026, 7, 1), Cents: 50000})

	_, err := f.svc.AssignMultipleBankRowsToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, []string{early, late}, inv.ID)
	if !errors.Is(err, ErrImportedNoOpeningBalance) {
		t.Fatalf("assign with the earliest row before the opening balance: err = %v, want ErrImportedNoOpeningBalance", err)
	}
	if f.bankRowBooked(t, early) || f.bankRowBooked(t, late) || f.paid(t, inv) {
		t.Fatalf("refused multi-assign booked something")
	}

	onTime := f.club.NewBankRow(t, importID, fin.BankRowOpts{Date: day(2026, 6, 20), Cents: 40000})
	if _, err := f.svc.AssignMultipleBankRowsToInvoice(ctx, f.club.ID, f.club.Treasurer.ID, []string{onTime, late}, inv.ID); err != nil {
		t.Fatalf("assign with every row after the opening balance: %v", err)
	}
	if !f.bankRowBooked(t, onTime) || !f.bankRowBooked(t, late) || !f.paid(t, inv) {
		t.Errorf("multi-assign after the opening balance did not book both rows and settle the faktura")
	}
}

// A Vipps charge assigned to an imported faktura obeys the same rule, with
// the charge's booking date.
func TestAssignVippsChargeToImportedFaktura(t *testing.T) {
	f := newImportedFixture(t)
	ctx := context.Background()
	inv := f.imported(t, day(2025, 9, 1), 45000)
	charge := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, Date: day(2026, 6, 4), CustomerName: "Synthetic Payer"})

	if _, err := f.svc.AssignVippsChargeToInvoice(ctx, f.club.ID, charge, inv.ID, f.club.Treasurer.ID); !errors.Is(err, ErrImportedNoOpeningBalance) {
		t.Fatalf("assign without an opening balance: err = %v, want ErrImportedNoOpeningBalance", err)
	}
	var booked bool
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT journal_entry_id IS NOT NULL FROM vipps_import_rows WHERE id = $1`, charge).Scan(&booked); err != nil {
		t.Fatalf("charge: %v", err)
	}
	if booked || f.paid(t, inv) {
		t.Fatalf("refused Vipps assign booked the charge %v or paid the faktura %v", booked, f.paid(t, inv))
	}

	f.openingBalance(t, day(2026, 1, 1), 45000)
	res, err := f.svc.AssignVippsChargeToInvoice(ctx, f.club.ID, charge, inv.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("assign with an opening balance: %v", err)
	}
	if res.EntryID == "" || !f.paid(t, inv) {
		t.Errorf("assign = %+v, faktura paid %v; want booked and paid", res, f.paid(t, inv))
	}
}

// The KID cascade's SELECTs never offer an imported faktura today (empty
// KID), so the lock's own check is exercised directly: it refuses without a
// booking date, and refuses an imported faktura without its receivable.
func TestLockInvoiceForAssignImportedFaktura(t *testing.T) {
	f := newImportedFixture(t)
	ctx := context.Background()
	inv := f.imported(t, day(2025, 9, 1), 30000)
	lock := func(opts invoiceLockOpts) error {
		tx, err := f.club.Pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = lockInvoiceForAssign(ctx, tx, f.club.ID, inv.ID, opts)
		return err
	}

	for _, mode := range []invoiceLockMode{lockManual, lockKIDCascade} {
		if err := lock(invoiceLockOpts{mode: mode}); err == nil || errors.Is(err, ErrImportedNoOpeningBalance) {
			t.Errorf("mode %d without a booking date: err = %v, want a caller error", mode, err)
		}
		if err := lock(invoiceLockOpts{mode: mode, bookingDate: day(2026, 6, 1)}); !errors.Is(err, ErrImportedNoOpeningBalance) {
			t.Errorf("mode %d without an opening balance: err = %v, want ErrImportedNoOpeningBalance", mode, err)
		}
	}
	f.openingBalance(t, day(2026, 1, 1), 30000)
	for _, mode := range []invoiceLockMode{lockManual, lockKIDCascade} {
		if err := lock(invoiceLockOpts{mode: mode, bookingDate: day(2026, 6, 1)}); err != nil {
			t.Errorf("mode %d with an opening balance: %v", mode, err)
		}
	}
}

// The legacy Vipps member match never offers an imported faktura whose
// receivable isn't in the GL by the charge date.
func TestFindOpenInvoiceForMemberSkipsImportedWithoutOpeningBalance(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, audit.NewService(pool, zerolog.Nop()), zerolog.Nop())
	now := time.Now().UTC()
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan), fin.WithYear(now.Year()))
	f := &importedFixture{svc: svc, club: club, member: club.NewMember(t, "member")}
	ctx := context.Background()

	// Recent, so the 90-day window applies; kept within this year's period.
	issued := now.AddDate(0, 0, -10)
	if issued.Year() != now.Year() {
		issued = day(now.Year(), 1, 1)
	}
	inv := f.imported(t, issued, 45000)

	if got := svc.findOpenInvoiceForMember(ctx, club.ID, f.member.ID, 450, now); got != "" {
		t.Fatalf("matched imported faktura %s without an opening balance", got)
	}
	f.openingBalance(t, issued, 45000)
	if got := svc.findOpenInvoiceForMember(ctx, club.ID, f.member.ID, 450, issued.AddDate(0, 0, -1)); got != "" {
		t.Errorf("matched imported faktura for a charge dated before the opening balance")
	}
	if got := svc.findOpenInvoiceForMember(ctx, club.ID, f.member.ID, 450, now); got != inv.ID {
		t.Errorf("match = %q, want the imported faktura %s once its opening balance exists", got, inv.ID)
	}
}

// An order-linked Vipps charge for an imported faktura (an order minted
// before checkout refused such fakturas) is refused the same way.
func TestAssignVippsChargeToOrderForImportedFaktura(t *testing.T) {
	f := newImportedFixture(t)
	ctx := context.Background()
	inv := f.imported(t, day(2025, 9, 1), 45000)
	order := f.club.NewOrderForInvoice(t, inv)
	charge := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, Date: day(2026, 6, 4), OrderID: order.VippsReference})

	if _, found, err := f.svc.AssignVippsChargeToOrder(ctx, f.club.ID, charge, f.club.Treasurer.ID); !found || !errors.Is(err, ErrImportedNoOpeningBalance) {
		t.Fatalf("assign to order: found %v, err %v; want found and ErrImportedNoOpeningBalance", found, err)
	}
	if f.paid(t, inv) {
		t.Fatalf("refused order charge paid the faktura")
	}

	f.openingBalance(t, day(2026, 1, 1), 45000)
	if _, _, err := f.svc.AssignVippsChargeToOrder(ctx, f.club.ID, charge, f.club.Treasurer.ID); err != nil {
		t.Fatalf("assign to order with an opening balance: %v", err)
	}
	if !f.paid(t, inv) {
		t.Errorf("order charge with an opening balance did not pay the faktura")
	}
}

// The legacy single-bilag Vipps confirm re-checks every faktura its lines
// settle rather than trusting the preview.
func TestReconcileVippsConfirmRefusesImportedFaktura(t *testing.T) {
	f := newImportedFixture(t)
	ctx := context.Background()
	inv := f.imported(t, day(2025, 9, 1), 45000)
	rowID := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Date: day(2026, 6, 5), Cents: 45000})
	lines := []VippsReconcileLine{
		{Kind: "bank_in", AccountCode: "1920", Debit: 450},
		{Kind: "receivable", AccountCode: receivablesAccountCode, Credit: 450, LinkedInvoiceID: inv.ID},
	}

	if _, err := f.svc.ReconcileVippsConfirm(ctx, f.club.ID, rowID, "", f.club.Treasurer.ID, lines); !errors.Is(err, ErrImportedNoOpeningBalance) {
		t.Fatalf("confirm: err = %v, want ErrImportedNoOpeningBalance", err)
	}
	if f.bankRowBooked(t, rowID) || f.paid(t, inv) {
		t.Fatalf("refused confirm booked the row or paid the faktura")
	}

	f.openingBalance(t, day(2026, 1, 1), 45000)
	if _, err := f.svc.ReconcileVippsConfirm(ctx, f.club.ID, rowID, "", f.club.Treasurer.ID, lines); err != nil {
		t.Fatalf("confirm with an opening balance: %v", err)
	}
	if !f.bankRowBooked(t, rowID) || !f.paid(t, inv) {
		t.Errorf("confirm with an opening balance did not book the row and pay the faktura")
	}
}
