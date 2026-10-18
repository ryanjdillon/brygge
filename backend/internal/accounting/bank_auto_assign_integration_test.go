package accounting

import (
	"context"
	"strings"

	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

type bankAutoFixture struct {
	svc    *Service
	club   *fin.Club
	imp    string
	payers int
}

func newBankAutoFixture(t *testing.T) *bankAutoFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, audit.NewService(pool, zerolog.Nop()), zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	f := &bankAutoFixture{svc: svc, club: club, imp: club.NewBankImport(t)}
	f.exec(t, `UPDATE clubs SET bank_auto_assign = true WHERE id = $1`, club.ID)
	return f
}

func (f *bankAutoFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.club.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// member seeds a member named "Synthetic <last>".
func (f *bankAutoFixture) member(t *testing.T, last string) fin.Member {
	t.Helper()
	m := f.club.NewMember(t, "member")
	f.exec(t, `UPDATE users SET first_name = 'Synthetic', last_name = $2 WHERE id = $1`, m.ID, last)
	return m
}

func (f *bankAutoFixture) faktura(t *testing.T, m fin.Member, cents int64) fin.Invoice {
	t.Helper()
	return f.club.NewInvoice(t, fin.InvoiceOpts{Member: &m, Lines: []fin.Line{{Category: "membership", Cents: cents}}})
}

// deposit seeds an incoming KID-less row from the payer, from a synthetic
// account of its own so no two payers look like the same party.
func (f *bankAutoFixture) deposit(t *testing.T, payer string, cents int64, o ...fin.BankRowOpts) string {
	t.Helper()
	var opts fin.BankRowOpts
	if len(o) > 0 {
		opts = o[0]
	}
	f.payers++
	opts.Cents = cents
	if opts.Counterpart == "" {
		opts.Counterpart = payer
	}
	if opts.FromAccount == "" {
		opts.FromAccount = "1234560" + strings.Repeat("0", 3) + string(rune('0'+f.payers%10))
	}
	return f.club.NewBankRow(t, f.imp, opts)
}

func (f *bankAutoFixture) run(t *testing.T) *AutoAssignResult {
	t.Helper()
	res, err := f.svc.AutoAssignBankDeposits(context.Background(), f.club.ID, f.club.Treasurer.ID,
		BankAutoAssignOpts{Trigger: BankAutoAssignTriggerSync})
	if err != nil {
		t.Fatalf("AutoAssignBankDeposits: %v", err)
	}
	return res
}

func (f *bankAutoFixture) entry(t *testing.T, rowID string) string {
	t.Helper()
	var entry *string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT journal_entry_id::text FROM bank_import_rows WHERE id = $1`, rowID).Scan(&entry); err != nil {
		t.Fatalf("bank row: %v", err)
	}
	if entry == nil {
		return ""
	}
	return *entry
}

func (f *bankAutoFixture) paid(t *testing.T, invoiceID string) bool {
	t.Helper()
	var paid bool
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT payment_id IS NOT NULL FROM invoices WHERE id = $1`, invoiceID).Scan(&paid); err != nil {
		t.Fatalf("invoice: %v", err)
	}
	return paid
}

func TestBankAutoAssignBooksUniqueMemberDeposit(t *testing.T) {
	f := newBankAutoFixture(t)
	ctx := context.Background()
	m := f.member(t, "Kaiplass")
	inv := f.faktura(t, m, 45000)
	row := f.deposit(t, "Synthetic Kaiplass", 45000)

	res := f.run(t)
	if len(res.Assigned) != 1 || res.Assigned[0].RowID != row || res.Assigned[0].InvoiceNumber != inv.Number ||
		res.Assigned[0].MatchPass != "full_name" || res.Assigned[0].Source != AutoAssignSourceBank {
		t.Fatalf("result = %+v, want the deposit booked to faktura %d", res, inv.Number)
	}
	entry := f.entry(t, row)
	if got := creditLines(t, f.club, entry); got["1500"][1] != 450 || got["1920"][0] != 450 {
		t.Errorf("entry lines = %v, want DR 1920 / CR 1500 450", got)
	}
	var status, desc, invoiceID string
	var autoMatched bool
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT je.status::text, je.description, b.invoice_id::text, b.auto_matched
		   FROM bank_import_rows b JOIN journal_entries je ON je.id = b.journal_entry_id WHERE b.id = $1`, row,
	).Scan(&status, &desc, &invoiceID, &autoMatched); err != nil {
		t.Fatalf("booking: %v", err)
	}
	if status != "posted" || !strings.HasPrefix(desc, "Automatisk tildeling") || invoiceID != inv.ID || autoMatched {
		t.Errorf("booking = %s %q invoice %s auto_matched %v; want posted, automatic, linked, auto_matched false", status, desc, invoiceID, autoMatched)
	}
	if !f.paid(t, inv.ID) {
		t.Errorf("faktura not marked paid")
	}
	var trigger, pass string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT details->>'trigger', details->>'match_pass' FROM audit_log WHERE action = $1 AND resource_id = $2`,
		audit.ActionBankRowAutoAssigned, row).Scan(&trigger, &pass); err != nil || trigger != BankAutoAssignTriggerSync || pass != "full_name" {
		t.Errorf("audit = (%q, %q), err %v; want one bank_sync row", trigger, pass, err)
	}
	var matched int
	if err := f.club.Pool.QueryRow(ctx, `SELECT matched_count FROM bank_imports WHERE id = $1`, f.imp).Scan(&matched); err != nil || matched != 1 {
		t.Errorf("matched_count = %d (err %v), want 1", matched, err)
	}

	// A rerun finds nothing left to book.
	if again := f.run(t); len(again.Assigned) != 0 {
		t.Errorf("rerun = %+v, want a no-op", again)
	}

	// Unassign reopens the faktura; the next sync books it again.
	if _, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, row); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if f.paid(t, inv.ID) || f.entry(t, row) != "" {
		t.Fatalf("unassign left the faktura paid or the row booked")
	}
	synced, err := f.svc.BankSync(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("bank sync: %v", err)
	}
	if synced.AutoAssign == nil || len(synced.AutoAssign.Assigned) != 1 || !f.paid(t, inv.ID) {
		t.Errorf("sync auto-assign = %+v, want the deposit booked again", synced.AutoAssign)
	}
}

func TestBankAutoAssignUsesTaughtAlias(t *testing.T) {
	f := newBankAutoFixture(t)
	m := f.member(t, "Fortoy")
	inv := f.faktura(t, m, 30000)
	f.exec(t, `INSERT INTO vipps_customer_aliases (club_id, customer_name, user_id) VALUES ($1, 'SYN FORTOY HOLDING', $2)`, f.club.ID, m.ID)
	row := f.deposit(t, "SYN FORTOY HOLDING", 30000)

	res := f.run(t)
	if len(res.Assigned) != 1 || res.Assigned[0].RowID != row || res.Assigned[0].MatchPass != "alias" || !f.paid(t, inv.ID) {
		t.Fatalf("result = %+v, want the deposit booked through the alias", res)
	}
}

// Every ambiguous case stays in Tildel with its reason.
func TestBankAutoAssignLeavesAmbiguousDepositsManual(t *testing.T) {
	f := newBankAutoFixture(t)
	paidOn := time.Date(f.club.Year, 6, 1, 0, 0, 0, 0, time.UTC)

	twinA, twinB := f.member(t, "Tvilling"), f.member(t, "Tvilling")
	f.faktura(t, twinA, 45000)
	f.faktura(t, twinB, 30000)
	sharedName := f.deposit(t, "Synthetic Tvilling", 45000)

	two := f.member(t, "Tofaktura")
	f.faktura(t, two, 45000)
	f.faktura(t, two, 45000)
	twoOpen := f.deposit(t, "Synthetic Tofaktura", 45000)

	ore := f.member(t, "Oremiss")
	f.faktura(t, ore, 45000)
	oneOre := f.deposit(t, "Synthetic Oremiss", 45001)

	later := f.member(t, "Seinare")
	f.club.NewInvoice(t, fin.InvoiceOpts{Member: &later, IssueDate: paidOn.AddDate(0, 0, 1), Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	early := f.deposit(t, "Synthetic Seinare", 45000, fin.BankRowOpts{Date: paidOn})

	dbl := f.member(t, "Dobbel")
	f.faktura(t, dbl, 45000)
	dblA := f.deposit(t, "Synthetic Dobbel", 45000)
	dblB := f.deposit(t, "Synthetic Dobbel", 45000, fin.BankRowOpts{Date: paidOn.AddDate(0, 0, 3)})

	stranger := f.deposit(t, "Synthetic Ukjend", 45000)

	ref := f.member(t, "Referanse")
	f.faktura(t, ref, 45000)
	withNumber := f.deposit(t, "Synthetic Referanse", 45000, fin.BankRowOpts{Description: "Faktura 4711"})
	kid := finance.GenerateKID("000", 4712, 1)
	withKID := f.deposit(t, "", 45000, fin.BankRowOpts{Description: "Fra: Synthetic Referanse Betalt: 01.06.26 · " + kid})

	noPayer := f.deposit(t, "", 45000, fin.BankRowOpts{Counterpart: " "})

	res := f.run(t)
	if len(res.Assigned) != 0 {
		t.Fatalf("booked %+v, want nothing", res.Assigned)
	}
	want := map[string]int{
		reasonNoStrongMember:        2, // shared name, unknown payer
		reasonSeveralOpenFakturas:   1,
		reasonAmountMismatch:        1,
		reasonFakturaAfterPayment:   1,
		reasonPossibleDoublePayment: 2,
		reasonHasReference:          2,
		reasonNoPayer:               1,
	}
	for reason, n := range want {
		if res.ByReason[reason] != n {
			t.Errorf("by_reason[%s] = %d, want %d (all: %v)", reason, res.ByReason[reason], n, res.ByReason)
		}
	}
	for _, row := range []string{sharedName, twoOpen, oneOre, early, dblA, dblB, stranger, withNumber, withKID, noPayer} {
		if f.entry(t, row) != "" {
			t.Errorf("row %s was booked", row)
		}
	}
}

// Fakturas the member no longer owes, and imported fakturas, are never
// targets.
func TestBankAutoAssignSkipsSettledAndImportedFakturas(t *testing.T) {
	f := newBankAutoFixture(t)
	rows := map[string]string{}
	for name, o := range map[string]fin.InvoiceOpts{
		"voided":         {Status: "voided"},
		"vipps-captured": {VippsPaid: true},
		"paid":           {Paid: true},
		"imported":       {Imported: true},
	} {
		m := f.member(t, "Settled"+strings.ReplaceAll(name, "-", ""))
		o.Member = &m
		o.Lines = []fin.Line{{Category: "membership", Cents: 45000}}
		f.club.NewInvoice(t, o)
		rows[name] = f.deposit(t, "Synthetic Settled"+strings.ReplaceAll(name, "-", ""), 45000)
	}
	credited := f.member(t, "Kreditert")
	inv := f.faktura(t, credited, 45000)
	cn := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &credited, Lines: []fin.Line{{Category: "membership", Cents: -45000}}})
	f.exec(t, `UPDATE invoices SET credits_invoice_id = $1 WHERE id = $2`, inv.ID, cn.ID)
	rows["credited"] = f.deposit(t, "Synthetic Kreditert", 45000)

	noteOnly := f.member(t, "Kreditnota")
	f.club.NewInvoice(t, fin.InvoiceOpts{Member: &noteOnly, Lines: []fin.Line{{Category: "membership", Cents: -45000}}})
	rows["credit note"] = f.deposit(t, "Synthetic Kreditnota", 45000)

	res := f.run(t)
	if len(res.Assigned) != 0 {
		t.Fatalf("booked %+v, want nothing", res.Assigned)
	}
	if res.ByReason[reasonImportedFaktura] != 1 {
		t.Errorf("by_reason = %v, want one imported_faktura", res.ByReason)
	}
	for name, row := range rows {
		if f.entry(t, row) != "" {
			t.Errorf("%s faktura: deposit booked", name)
		}
	}
}

// Rows that are not candidates at all, and the club paying itself.
func TestBankAutoAssignIgnoresIneligibleRows(t *testing.T) {
	f := newBankAutoFixture(t)
	ctx := context.Background()
	f.exec(t, `UPDATE clubs SET name = 'Synthetic Harbour Club' WHERE id = $1`, f.club.ID)
	own := f.member(t, "Harbour Club")
	f.faktura(t, own, 45000)
	ownRow := f.deposit(t, "Synthetic Harbour Club", 45000, fin.BankRowOpts{FromAccount: " "})

	m := f.member(t, "Avvist")
	f.faktura(t, m, 45000)
	dismissed := f.deposit(t, "Synthetic Avvist", 45000)
	if err := f.svc.DismissBankRow(ctx, f.club.ID, f.club.Treasurer.ID, dismissed, "duplicate"); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	payout := f.deposit(t, "Synthetic Avvist", 45000, fin.BankRowOpts{Description: "Utb. 1234 Vippsnr 567890"})

	// A provisional in-flight row: bare text, nothing identifying, on the
	// statement's last day.
	f.exec(t, `UPDATE bank_imports SET period_to = make_date($2, 6, 30) WHERE id = $1`, f.imp, f.club.Year)
	f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Cents: 45000, Date: time.Date(f.club.Year, 6, 30, 0, 0, 0, 0, time.UTC), Description: "Overførsel"})

	booked := f.member(t, "Bokfort")
	f.faktura(t, booked, 45000)
	bookedRow := f.deposit(t, "Synthetic Bokfort", 45000)
	if _, err := f.svc.AssignBankRowToAccount(ctx, f.club.ID, f.club.Treasurer.ID, bookedRow, "3100", "revenue", "synthetic"); err != nil {
		t.Fatalf("book row: %v", err)
	}
	bookedEntry := f.entry(t, bookedRow)

	res := f.run(t)
	if len(res.Assigned) != 0 || res.ByReason[reasonOwnClub] != 1 {
		t.Fatalf("result = %+v, want nothing booked and one own_club", res)
	}
	// Only the club's own deposit was a candidate.
	if res.NeedsReview != 1 {
		t.Errorf("needs_review = %d, want 1 (dismissed, payout, provisional and booked rows are no candidates)", res.NeedsReview)
	}
	if f.entry(t, ownRow) != "" || f.entry(t, dismissed) != "" || f.entry(t, payout) != "" || f.entry(t, bookedRow) != bookedEntry {
		t.Errorf("an ineligible row changed")
	}
}

// A deposit with a strong partner (BRY-380) may belong with another row.
func TestBankAutoAssignSkipsRowWithStrongPartner(t *testing.T) {
	f := newBankAutoFixture(t)
	m := f.member(t, "Partnar")
	f.faktura(t, m, 45000)
	day := time.Date(f.club.Year, 6, 1, 0, 0, 0, 0, time.UTC)
	row := f.deposit(t, "Synthetic Partnar", 45000, fin.BankRowOpts{Date: day, FromAccount: "55550012345"})
	f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Cents: -45000, Date: day.AddDate(0, 0, 2), ToAccount: "55550012345", Counterpart: "Synthetic Partnar"})

	res := f.run(t)
	if len(res.Assigned) != 0 || res.ByReason[reasonHasPartner] != 1 || f.entry(t, row) != "" {
		t.Fatalf("result = %+v, want the deposit left for review as has_partner", res)
	}
}

func TestBankAutoAssignPeriods(t *testing.T) {
	f := newBankAutoFixture(t)
	prev := f.club.Year - 1
	f.exec(t, `INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
	           VALUES ($1, $2, make_date($2, 1, 1), make_date($2, 12, 31), 'closed')`, f.club.ID, prev)

	closed := f.member(t, "Lukka")
	f.club.NewInvoice(t, fin.InvoiceOpts{Member: &closed, IssueDate: time.Date(prev, 3, 1, 0, 0, 0, 0, time.UTC), Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	closedRow := f.deposit(t, "Synthetic Lukka", 45000, fin.BankRowOpts{Date: time.Date(prev, 6, 1, 0, 0, 0, 0, time.UTC)})

	none := f.member(t, "Utanar")
	f.club.NewInvoice(t, fin.InvoiceOpts{Member: &none, IssueDate: time.Date(prev-1, 3, 1, 0, 0, 0, 0, time.UTC), Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	noneRow := f.deposit(t, "Synthetic Utanar", 45000, fin.BankRowOpts{Date: time.Date(prev-1, 6, 1, 0, 0, 0, 0, time.UTC)})

	res := f.run(t)
	if len(res.Assigned) != 0 || res.ByReason[reasonPeriodClosed] != 1 || res.ByReason[reasonNoPeriod] != 1 {
		t.Fatalf("result = %+v, want period_closed and no_period", res)
	}
	if f.entry(t, closedRow) != "" || f.entry(t, noneRow) != "" {
		t.Errorf("a row outside an open period was booked")
	}
	var periods int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM fiscal_periods WHERE club_id = $1`, f.club.ID).Scan(&periods); err != nil || periods != 2 {
		t.Errorf("fiscal periods = %d (err %v), want 2: the rule never creates one", periods, err)
	}
}

// A KID-booked bank row whose payment back-link never landed still counts
// as a payment against the faktura.
func TestBankAutoAssignSeesKIDBookingWithoutBackLink(t *testing.T) {
	f := newBankAutoFixture(t)
	ctx := context.Background()
	m := f.member(t, "Utanlenkje")
	inv := f.faktura(t, m, 45000)
	kidRow := f.club.NewBankRow(t, f.imp, fin.BankRowOpts{Cents: 20000, KID: inv.KID})
	if _, err := f.svc.AssignBankRowToAccount(ctx, f.club.ID, f.club.Treasurer.ID, kidRow, "3100", "revenue", "synthetic part"); err != nil {
		t.Fatalf("book KID row: %v", err)
	}
	row := f.deposit(t, "Synthetic Utanlenkje", 45000)

	res := f.run(t)
	if len(res.Assigned) != 0 || res.ByReason[reasonFakturaPartlyPaid] != 1 || f.entry(t, row) != "" {
		t.Fatalf("result = %+v, want faktura_partly_paid", res)
	}
}

// A second deposit after the first was auto-booked finds the faktura paid.
func TestBankAutoAssignCrossRunDoublePayment(t *testing.T) {
	f := newBankAutoFixture(t)
	m := f.member(t, "Tvigong")
	f.faktura(t, m, 45000)
	first := f.deposit(t, "Synthetic Tvigong", 45000)
	if res := f.run(t); len(res.Assigned) != 1 || res.Assigned[0].RowID != first {
		t.Fatalf("first run = %+v, want the first deposit booked", res)
	}
	second := f.deposit(t, "Synthetic Tvigong", 45000, fin.BankRowOpts{Date: time.Date(f.club.Year, 7, 1, 0, 0, 0, 0, time.UTC)})
	res := f.run(t)
	if len(res.Assigned) != 0 || res.ByReason[reasonNoOpenFaktura] != 1 || f.entry(t, second) != "" {
		t.Fatalf("second run = %+v, want the second deposit left as no_open_faktura", res)
	}
}

// Opt-in off books nothing, but the preview still projects the deposit and
// reports the real setting. With no acting user nothing is booked either.
func TestBankAutoAssignOptInPreviewAndActor(t *testing.T) {
	f := newBankAutoFixture(t)
	ctx := context.Background()
	f.exec(t, `UPDATE clubs SET bank_auto_assign = false WHERE id = $1`, f.club.ID)
	m := f.member(t, "Forhand")
	inv := f.faktura(t, m, 45000)
	row := f.deposit(t, "Synthetic Forhand", 45000)

	if res := f.run(t); res.Enabled || len(res.Assigned) != 0 || f.entry(t, row) != "" {
		t.Fatalf("result = %+v, want disabled with nothing booked", res)
	}
	if res := f.svc.AutoAssignBankDepositsReported(ctx, f.club.ID, f.club.Treasurer.ID, BankAutoAssignOpts{}); res != nil {
		t.Errorf("reported run while off = %+v, want nil", res)
	}

	p, err := f.svc.PreviewBankAutoAssign(ctx, f.club.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if p.Enabled || p.Candidates != 1 || p.WouldAssign != 1 || p.WouldAssignAmount != 450 ||
		len(p.Items) != 1 || p.Items[0].RowID != row || p.Items[0].InvoiceNumber != inv.Number {
		t.Fatalf("preview = %+v, want one deposit for faktura %d", p, inv.Number)
	}
	if f.entry(t, row) != "" || f.paid(t, inv.ID) {
		t.Errorf("preview booked the deposit")
	}

	f.exec(t, `UPDATE clubs SET bank_auto_assign = true WHERE id = $1`, f.club.ID)
	res, err := f.svc.AutoAssignBankDeposits(ctx, f.club.ID, "", BankAutoAssignOpts{Trigger: BankAutoAssignTriggerImport})
	if err != nil {
		t.Fatalf("run without actor: %v", err)
	}
	if len(res.Assigned) != 0 || res.ByReason[reasonNoActor] != 1 || f.entry(t, row) != "" {
		t.Errorf("result without actor = %+v, want no_actor and nothing booked", res)
	}
}

// statement is a two-row synthetic statement: a 450 kr deposit from the
// payer's account and a fee paid from the club's own account, so the import
// infers the club's account number the way it does from a real statement.
func statement(year int, payer, payerAccount string) []BankRow {
	const own = "98760011111"
	day := time.Date(year, 6, 2, 0, 0, 0, 0, time.UTC)
	return []BankRow{
		{Date: day, Amount: 450, Description: "Synthetic giro", Counterpart: payer,
			Reference: "SYNREF" + testutil.RandomHex(4), FromAccount: payerAccount, ToAccount: own},
		{Date: day, Amount: -12, Description: "Synthetic fee", Counterpart: "Synthetic Bank",
			Reference: "SYNREF" + testutil.RandomHex(4), FromAccount: own, ToAccount: "11112222333"},
	}
}

// The import runs the rule with its importing user; with the switch off,
// the older name+amount cascade step still books, and with it on that step
// stands down for the rule.
func TestBankAutoAssignAtImportReplacesNameStep(t *testing.T) {
	ctx := context.Background()
	for _, on := range []bool{false, true} {
		f := newBankAutoFixture(t)
		f.exec(t, `UPDATE clubs SET bank_auto_assign = $2 WHERE id = $1`, f.club.ID, on)
		m := f.member(t, "Importert")
		inv := f.faktura(t, m, 45000)
		imp := f.club.NewBankImport(t)
		res, err := f.svc.ImportBankRows(ctx, f.club.ID, imp, "", statement(f.club.Year, "Synthetic Importert", "77770012345"))
		if err != nil {
			t.Fatalf("import (switch %v): %v", on, err)
		}
		if !f.paid(t, inv.ID) {
			t.Errorf("switch %v: faktura not paid after import", on)
		}
		if on {
			if res.Matched != 0 || res.AutoAssign == nil || len(res.AutoAssign.Assigned) != 1 {
				t.Errorf("switch on: matched %d, auto %+v; want the rule, not the cascade, to book", res.Matched, res.AutoAssign)
			}
			var trigger string
			if err := f.club.Pool.QueryRow(ctx,
				`SELECT details->>'trigger' FROM audit_log WHERE action = $1`, audit.ActionBankRowAutoAssigned,
			).Scan(&trigger); err != nil || trigger != BankAutoAssignTriggerImport {
				t.Errorf("audit trigger = %q (err %v), want bank_import", trigger, err)
			}
		} else if res.Matched != 1 || res.AutoAssign != nil {
			t.Errorf("switch off: matched %d, auto %+v; want the cascade's name step to book", res.Matched, res.AutoAssign)
		}
	}
}

// An import with a chosen period posts its own rows there.
func TestBankAutoAssignImportPeriodOverride(t *testing.T) {
	f := newBankAutoFixture(t)
	ctx := context.Background()
	var override string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, $2, make_date($2, 1, 1), make_date($2, 12, 31), 'open') RETURNING id`,
		f.club.ID, f.club.Year+1).Scan(&override); err != nil {
		t.Fatalf("period: %v", err)
	}
	m := f.member(t, "Overstyrt")
	f.faktura(t, m, 45000)
	imp := f.club.NewBankImport(t)
	res, err := f.svc.ImportBankRows(ctx, f.club.ID, imp, override, statement(f.club.Year, "Synthetic Overstyrt", "77770054321"))
	if err != nil || res.AutoAssign == nil || len(res.AutoAssign.Assigned) != 1 {
		t.Fatalf("import = %+v, err %v; want the deposit booked", res.AutoAssign, err)
	}
	var period string
	if err := f.club.Pool.QueryRow(ctx, `SELECT fiscal_period_id::text FROM journal_entries WHERE id = $1`,
		res.AutoAssign.Assigned[0].EntryID).Scan(&period); err != nil || period != override {
		t.Errorf("entry period = %s (err %v), want the override %s", period, err, override)
	}
	var matched int
	if err := f.club.Pool.QueryRow(ctx, `SELECT matched_count FROM bank_imports WHERE id = $1`, imp).Scan(&matched); err != nil || matched != 1 {
		t.Errorf("matched_count = %d (err %v), want 1", matched, err)
	}
}
