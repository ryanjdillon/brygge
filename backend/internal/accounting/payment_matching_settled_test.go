package accounting

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// matchingFixture is a club with fakturas in every state a bank or Vipps
// payment can meet: open, voided, captured by a Vipps pay link (awaiting
// settlement), credited by a linked credit note, and already paid.
type matchingFixture struct {
	svc      *Service
	club     *fin.Club
	open     fin.Invoice
	voided   fin.Invoice
	vipps    fin.Invoice
	credited fin.Invoice
	paid     fin.Invoice
}

func newMatchingFixture(t *testing.T) *matchingFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, audit.NewService(pool, zerolog.Nop()), zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	f := &matchingFixture{svc: svc, club: club}

	mixed := func(o fin.InvoiceOpts) fin.Invoice {
		m := club.NewMember(t, "member")
		o.Member = &m
		o.Lines = []fin.Line{{Category: "membership", Cents: 45000}, {Category: "slip_fee", Cents: 255000}}
		// Recent, so the 90-day window of the Vipps member match applies.
		o.IssueDate = time.Now().AddDate(0, 0, -10)
		return club.NewInvoice(t, o)
	}
	f.open = mixed(fin.InvoiceOpts{})
	f.voided = mixed(fin.InvoiceOpts{Status: "voided"})
	f.vipps = mixed(fin.InvoiceOpts{VippsPaid: true})
	f.credited = mixed(fin.InvoiceOpts{})
	f.paid = mixed(fin.InvoiceOpts{Paid: true})

	m := fin.Member{ID: f.credited.MemberID}
	cn := club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Lines: []fin.Line{{Category: "slip_fee", Cents: -300000}}})
	if _, err := pool.Exec(context.Background(),
		`UPDATE invoices SET credits_invoice_id = $1 WHERE id = $2`, f.credited.ID, cn.ID); err != nil {
		t.Fatalf("link credit note: %v", err)
	}
	return f
}

func (f *matchingFixture) paymentID(t *testing.T, inv fin.Invoice) bool {
	t.Helper()
	var paid bool
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT payment_id IS NOT NULL FROM invoices WHERE id = $1`, inv.ID).Scan(&paid); err != nil {
		t.Fatalf("invoice: %v", err)
	}
	return paid
}

func (f *matchingFixture) rowBooked(t *testing.T, kid string) bool {
	t.Helper()
	var booked bool
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT journal_entry_id IS NOT NULL FROM bank_import_rows WHERE club_id = $1 AND kid_number = $2`,
		f.club.ID, kid).Scan(&booked); err != nil {
		t.Fatalf("bank row for %s: %v", kid, err)
	}
	return booked
}

// A bank KID payment only settles a faktura the member still owes. Money
// arriving for a voided, Vipps-captured, credited or already paid faktura is
// an overpayment a human must see, so the row stays in Tildel.
func TestImportKIDMatchesOnlyOwedFakturas(t *testing.T) {
	f := newMatchingFixture(t)
	importID := f.club.NewBankImport(t)
	var rows []BankRow
	for _, inv := range []fin.Invoice{f.open, f.voided, f.vipps, f.credited, f.paid} {
		rows = append(rows, BankRow{
			Date: time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC), Description: "Innbetaling",
			Amount: fin.Kr(inv.TotalCents), Reference: "SYN" + testutil.RandomHex(4), KID: inv.KID,
		})
	}
	res, err := f.svc.ImportBankRows(context.Background(), f.club.ID, importID, "", rows)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Matched != 1 || len(res.RowErrors) != 0 {
		t.Fatalf("import = matched %d, errors %+v; want only the open faktura matched", res.Matched, res.RowErrors)
	}
	if !f.rowBooked(t, f.open.KID) || !f.paymentID(t, f.open) {
		t.Errorf("the open faktura was not settled")
	}
	for name, inv := range map[string]fin.Invoice{"voided": f.voided, "vipps-captured": f.vipps, "credited": f.credited} {
		if f.rowBooked(t, inv.KID) {
			t.Errorf("%s faktura: bank row was booked, want it left for review", name)
		}
		if f.paymentID(t, inv) {
			t.Errorf("%s faktura: marked paid by a bank payment", name)
		}
	}
	if f.rowBooked(t, f.paid.KID) {
		t.Errorf("already paid faktura: second payment was booked against it")
	}
}

// The on-demand bank sync applies the same rule to rows already imported.
func TestBankSyncKIDMatchesOnlyOwedFakturas(t *testing.T) {
	f := newMatchingFixture(t)
	importID := f.club.NewBankImport(t)
	for _, inv := range []fin.Invoice{f.open, f.voided, f.vipps, f.credited, f.paid} {
		f.club.NewBankRow(t, importID, fin.BankRowOpts{Cents: inv.TotalCents, KID: inv.KID})
	}
	res, err := f.svc.BankSync(context.Background(), f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("bank sync: %v", err)
	}
	if res.KIDMatched != 1 {
		t.Fatalf("bank sync matched %d, want only the open faktura", res.KIDMatched)
	}
	for name, inv := range map[string]fin.Invoice{"voided": f.voided, "vipps-captured": f.vipps, "credited": f.credited, "paid": f.paid} {
		if f.rowBooked(t, inv.KID) {
			t.Errorf("%s faktura: bank row was booked", name)
		}
	}
}

// Two bank syncs racing on the same row book it exactly once and leave no
// orphan draft: the row link is taken before posting and guarded on the row
// still being unbooked.
func TestConcurrentBankSyncsBookARowOnce(t *testing.T) {
	f := newMatchingFixture(t)
	importID := f.club.NewBankImport(t)
	row := f.club.NewBankRow(t, importID, fin.BankRowOpts{Cents: f.open.TotalCents, KID: f.open.KID})

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.svc.BankSync(context.Background(), f.club.ID, f.club.Treasurer.ID); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("bank sync: %v", err)
	}

	var posted, drafts int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FILTER (WHERE status = 'posted'), count(*) FILTER (WHERE status = 'draft')
		   FROM journal_entries WHERE club_id = $1 AND source = 'bank_import'`, f.club.ID,
	).Scan(&posted, &drafts); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	if posted != 1 || drafts != 0 {
		t.Errorf("entries = %d posted, %d drafts; want exactly one posted entry for row %s", posted, drafts, row)
	}
}

// Vipps matching for a member uses the same "owed" rule: a credited or
// Vipps-captured faktura is never offered to a walk-up charge.
func TestVippsMemberMatchingSkipsSettledFakturas(t *testing.T) {
	f := newMatchingFixture(t)
	ctx := context.Background()
	for name, inv := range map[string]fin.Invoice{"credited": f.credited, "vipps-captured": f.vipps, "voided": f.voided, "paid": f.paid} {
		if got := f.svc.findOpenInvoiceForMember(ctx, f.club.ID, inv.MemberID, fin.Kr(inv.TotalCents), time.Now()); got != "" {
			t.Errorf("%s faktura offered to a Vipps charge (%s)", name, got)
		}
		mi, err := f.svc.decideMemberInvoice(ctx, f.club.ID, inv.MemberID, inv.TotalCents, time.Date(f.club.Year, 12, 31, 0, 0, 0, 0, time.UTC))
		if err != nil || mi.InvoiceID != "" || mi.SameAmount != 0 || mi.Reason != reasonNoOpenFaktura {
			t.Errorf("%s faktura: auto-assign decided %+v, err %v; want no open faktura", name, mi, err)
		}
	}
	if got := f.svc.findOpenInvoiceForMember(ctx, f.club.ID, f.open.MemberID, fin.Kr(f.open.TotalCents), time.Now()); got != f.open.ID {
		t.Errorf("open faktura not offered: got %q", got)
	}

	// Nothing the member is chased for: the credited faktura isn't owed, so
	// reminders skip it.
	var owed bool
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT `+finance.OwedByMemberSQL("i")+` FROM invoices i WHERE i.id = $1`, f.credited.ID,
	).Scan(&owed); err != nil || owed {
		t.Errorf("credited faktura owed = %v (err %v), want false", owed, err)
	}
}
