package accounting

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

func (f *refFixture) refDay(month, day int) time.Time {
	return time.Date(f.refYear, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

func (f *refFixture) curDay(month, day int) time.Time {
	return time.Date(f.club.Year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

func (f *refFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.club.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func rowIDs(rows []BankRowSummary) map[string]BankRowSummary {
	out := map[string]BankRowSummary{}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out
}

// A reference year is out of the default Tildel list, its count and the year
// dropdown's counts; the reference kind lists only its outgoing rows.
func TestTildelScopeExcludesReferenceRows(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()
	imp := f.club.NewBankImport(t)
	refIn := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(6, 1), Cents: 50000})
	refOut := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(6, 2), Cents: -20000})
	refOutDismissed := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(6, 3), Cents: -30000})
	f.exec(t, `UPDATE bank_import_rows SET dismissed_at = now(), dismissed_by = $2, dismissed_reason = 'internal_transfer' WHERE id = $1`,
		refOutDismissed, f.club.Treasurer.ID)
	curIn := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.curDay(2, 1), Cents: 10000})
	curOut := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.curDay(2, 2), Cents: -5000})

	list := func(kind string) map[string]BankRowSummary {
		t.Helper()
		rows, err := f.svc.ListUnmatchedBankRows(ctx, f.club.ID, kind, "", 0, 50, 0)
		if err != nil {
			t.Fatalf("list %q: %v", kind, err)
		}
		return rowIDs(rows)
	}
	count := func(kind string, year int) int {
		t.Helper()
		n, err := f.svc.CountUnmatchedBankRowsKind(ctx, f.club.ID, kind, year)
		if err != nil {
			t.Fatalf("count %q: %v", kind, err)
		}
		return n
	}

	// Before marking, the year is an ordinary one.
	if got := list(""); len(got) != 4 {
		t.Fatalf("unmarked default list has %d rows, want the 4 undismissed", len(got))
	}

	f.mark(t, true)

	got := list("")
	if len(got) != 2 || got[curIn].ID == "" || got[curOut].ID == "" {
		t.Errorf("default list = %v, want only the current year's two rows", got)
	}
	if n := count("", 0); n != 2 {
		t.Errorf("badge count = %d, want 2", n)
	}
	if n := count("", f.refYear); n != 0 {
		t.Errorf("reference year count = %d, want 0", n)
	}
	byYear, err := f.svc.CountUnmatchedBankRowsByYear(ctx, f.club.ID)
	if err != nil {
		t.Fatalf("by year: %v", err)
	}
	if _, ok := byYear[f.refYear]; ok || byYear[f.club.Year] != 2 {
		t.Errorf("by-year counts = %v, want only %d: 2", byYear, f.club.Year)
	}
	if got := list("dismissed"); len(got) != 0 {
		t.Errorf("dismissed list = %v, want the reference year's dismissed row left out too", got)
	}
	if got := list("incoming"); len(got) != 1 || got[curIn].ID == "" {
		t.Errorf("incoming list = %v, want only the current year's deposit", got)
	}

	ref := list("reference")
	if len(ref) != 1 || !ref[refOut].ReferencePeriod {
		t.Errorf("reference list = %v, want only the outgoing undismissed reference row %s", ref, refOut)
	}
	if _, ok := ref[refIn]; ok {
		t.Errorf("the reference year's incoming row is listed")
	}
	if n := count("reference", 0); n != 1 {
		t.Errorf("reference count = %d, want 1", n)
	}
	if _, err := f.svc.ListUnmatchedBankRows(ctx, f.club.ID, "bogus", "", 0, 50, 0); !errors.Is(err, ErrInvalidBankRowKind) {
		t.Errorf("unknown kind: err = %v, want ErrInvalidBankRowKind", err)
	}
	if _, err := f.svc.CountUnmatchedBankRowsKind(ctx, f.club.ID, "reference,bogus", 0); !errors.Is(err, ErrInvalidBankRowKind) {
		t.Errorf("count with an unknown kind: err = %v, want ErrInvalidBankRowKind", err)
	}

	f.mark(t, false)
	if n := count("", 0); n != 4 {
		t.Errorf("after unmarking the badge count = %d, want 4", n)
	}
}

// Another club's reference year affects neither this club's queue nor its
// KID cascade: the predicate is correlated to the row's own club.
func TestReferenceYearOfAnotherClubIsIgnored(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()
	other := fin.NewClub(t, f.club.Pool, fin.WithKontoplan(f.svc.SeedKontoplan), fin.WithYear(f.club.Year))
	otherPeriod := addPeriod(t, other, f.refYear)
	if _, _, err := f.svc.SetPeriodReference(ctx, other.ID, otherPeriod, true); err != nil {
		t.Fatalf("mark the other club's year: %v", err)
	}

	member := f.club.NewMember(t, "member")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &member, Cents: 45000})
	imp := f.club.NewBankImport(t)
	row := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(7, 1), Cents: 45000, KID: inv.KID})

	if n, err := f.svc.CountUnmatchedBankRowsKind(ctx, f.club.ID, "", 0); err != nil || n != 1 {
		t.Fatalf("count = %d (%v), want this club's row listed", n, err)
	}
	res, err := f.svc.BankSync(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("bank sync: %v", err)
	}
	if res.KIDMatched != 1 {
		t.Errorf("KID cascade matched %d rows, want 1 (the other club's reference year must not block it)", res.KIDMatched)
	}
	if n := f.scalar(t, `SELECT count(*) FROM bank_import_rows WHERE id = $1 AND journal_entry_id IS NOT NULL`, row); n != 1 {
		t.Errorf("row not booked")
	}
}

// BankSync leaves a reference year's deposit alone, whatever its KID says.
func TestBankSyncSkipsReferenceYearKIDRow(t *testing.T) {
	f := newRefFixture(t, true)
	ctx := context.Background()
	member := f.club.NewMember(t, "member")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &member, Cents: 45000})
	imp := f.club.NewBankImport(t)
	row := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(7, 1), Cents: 45000, KID: inv.KID})

	res, err := f.svc.BankSync(ctx, f.club.ID, f.club.Treasurer.ID)
	if err != nil {
		t.Fatalf("bank sync: %v", err)
	}
	if res.KIDMatched != 0 {
		t.Errorf("KID cascade matched %d rows in a reference year, want 0", res.KIDMatched)
	}
	if n := f.scalar(t, `SELECT count(*) FROM bank_import_rows WHERE id = $1 AND journal_entry_id IS NULL`, row); n != 1 {
		t.Errorf("reference row was booked")
	}
	if n := f.scalar(t, `SELECT count(*) FROM invoices WHERE id = $1 AND payment_id IS NULL`, inv.ID); n != 1 {
		t.Errorf("faktura was marked paid by a reference-year deposit")
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1 AND fiscal_period_id = $2`, f.club.ID, f.refPeriod); n != 0 {
		t.Errorf("sync wrote %d entries into the reference year", n)
	}
}

// Bank auto-assign neither books nor previews a reference-year deposit, nor
// one an import override would post into a reference year.
func TestBankAutoAssignSkipsReferenceRows(t *testing.T) {
	f := newRefFixture(t, true)
	ctx := context.Background()
	f.exec(t, `UPDATE clubs SET bank_auto_assign = true WHERE id = $1`, f.club.ID)
	member := f.club.NewMember(t, "member")
	f.exec(t, `UPDATE users SET first_name = 'Synthetic', last_name = 'Kaiplass' WHERE id = $1`, member.ID)
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &member, IssueDate: f.refDay(2, 1),
		Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	imp := f.club.NewBankImport(t)
	row := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(7, 1), Cents: 45000,
		Counterpart: "Synthetic Kaiplass", FromAccount: "12345600001"})

	res, err := f.svc.AutoAssignBankDeposits(ctx, f.club.ID, f.club.Treasurer.ID, BankAutoAssignOpts{Trigger: BankAutoAssignTriggerSync})
	if err != nil {
		t.Fatalf("auto-assign: %v", err)
	}
	if len(res.Assigned) != 0 {
		t.Errorf("auto-assign booked %d reference-year deposits", len(res.Assigned))
	}
	preview, err := f.svc.PreviewBankAutoAssign(ctx, f.club.ID)
	if err != nil || preview.Candidates != 0 {
		t.Errorf("preview = %+v (%v), want no candidates", preview, err)
	}

	// The same deposit in the current year is booked, so the skip is the
	// reference year's and not a flaw of the fixture.
	f.mark(t, false)
	res, err = f.svc.AutoAssignBankDeposits(ctx, f.club.ID, f.club.Treasurer.ID, BankAutoAssignOpts{Trigger: BankAutoAssignTriggerSync})
	if err != nil || len(res.Assigned) != 1 || res.Assigned[0].RowID != row {
		t.Fatalf("unmarked auto-assign = %+v (%v), want the deposit booked to faktura %d", res, err, inv.Number)
	}
}

func TestBankAutoAssignSkipsOverrideIntoReferencePeriod(t *testing.T) {
	f := newRefFixture(t, true)
	ctx := context.Background()
	f.exec(t, `UPDATE clubs SET bank_auto_assign = true WHERE id = $1`, f.club.ID)
	member := f.club.NewMember(t, "member")
	f.exec(t, `UPDATE users SET first_name = 'Synthetic', last_name = 'Kaiplass' WHERE id = $1`, member.ID)
	f.club.NewInvoice(t, fin.InvoiceOpts{Member: &member, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	imp := f.club.NewBankImport(t)
	f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.curDay(3, 1), Cents: 45000,
		Counterpart: "Synthetic Kaiplass", FromAccount: "12345600001"})

	res, err := f.svc.AutoAssignBankDeposits(ctx, f.club.ID, f.club.Treasurer.ID,
		BankAutoAssignOpts{Trigger: BankAutoAssignTriggerImport, ImportID: imp, PeriodOverride: f.refPeriod})
	if err != nil {
		t.Fatalf("auto-assign: %v", err)
	}
	if len(res.Assigned) != 0 {
		t.Errorf("auto-assign booked %d rows into a reference year through an import override", len(res.Assigned))
	}
}

// An intra-bank pair dated in a reference year is not drafted.
func TestTransferDetectionSkipsReferencePair(t *testing.T) {
	f := newRefFixture(t, true)
	ctx := context.Background()
	impA := f.club.NewBankImportOn(t, "1920")
	impB := f.club.NewBankImportOn(t, "1925")
	f.club.NewBankRow(t, impA, fin.BankRowOpts{Date: f.refDay(5, 5), Cents: -100000})
	f.club.NewBankRow(t, impB, fin.BankRowOpts{Date: f.refDay(5, 5), Cents: 100000})

	matched, _, err := f.svc.detectIntraBankTransfers(ctx, f.club.ID, impA, "", f.club.Treasurer.ID)
	if err != nil || matched != 0 {
		t.Fatalf("reference pair: matched %d (%v), want 0", matched, err)
	}

	f.mark(t, false)
	matched, _, err = f.svc.detectIntraBankTransfers(ctx, f.club.ID, impA, "", f.club.Treasurer.ID)
	if err != nil || matched != 1 {
		t.Fatalf("unmarked pair: matched %d (%v), want 1", matched, err)
	}
}

// The mapping rules do not draft a reference-year row, in the caller's
// period or any other.
func TestAutoMatchImportSkipsReferenceRows(t *testing.T) {
	f := newRefFixture(t, true)
	ctx := context.Background()
	if _, err := f.svc.CreateRule(ctx, f.club.ID, CreateRuleInput{
		Name: "Synthetic fee rule", Priority: 1, MatchField: "description", MatchOperator: "like", MatchValue: "%SYNTHFEE%",
		DebitAccountCode: "6200", CreditAccountCode: "1920", MVAEligible: MVANotApplicable,
	}); err != nil {
		t.Fatalf("rule: %v", err)
	}
	imp := f.club.NewBankImport(t)
	refRow := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(4, 4), Cents: -12000, Description: "SYNTHFEE one"})
	f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.curDay(4, 4), Cents: -13000, Description: "SYNTHFEE two"})

	matched, err := f.svc.AutoMatchImport(ctx, f.club.ID, imp, f.club.PeriodID, f.club.Treasurer.ID)
	if err != nil || matched != 1 {
		t.Fatalf("auto-match matched %d (%v), want only the current year's row", matched, err)
	}
	if n := f.scalar(t, `SELECT count(*) FROM bank_import_rows WHERE id = $1 AND journal_entry_id IS NULL`, refRow); n != 1 {
		t.Errorf("reference row was drafted")
	}
}

// Nothing books 2025 income into another year by naming that year: a manual
// match, a mapping rule with another period and an import override all carry
// the row's own date, which the guard reads.
func TestMatchBankRowIntoOpenYearRefusesReferenceDate(t *testing.T) {
	f := newRefFixture(t, true)
	ctx := context.Background()
	imp := f.club.NewBankImport(t)
	deposit := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(8, 8), Cents: 70000})
	outgoing := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(8, 9), Cents: -9000})

	err := f.svc.MatchBankRow(ctx, f.club.ID, f.club.PeriodID, deposit, "1920", "3100", f.club.Treasurer.ID, 0)
	if !errors.Is(err, ErrReferencePeriod) {
		t.Fatalf("match a reference deposit into the open year: err = %v, want ErrReferencePeriod", err)
	}
	if err := f.svc.MatchBankRow(ctx, f.club.ID, f.club.PeriodID, outgoing, "6200", "1920", f.club.Treasurer.ID, 0); err != nil {
		t.Fatalf("match a reference outgoing row to a cost account: %v", err)
	}
}

// The bank-side reassign handler and the posted-line updates it makes live
// outside journal.go; the passes that delete a draft and rebook it must leave
// a reference year's drafts alone.
func TestDraftRebookingPassesKeepReferenceDrafts(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()
	imp := f.club.NewBankImport(t)

	// A stranded KID draft and a legacy Vipps bilag draft, both in the year.
	strandedRow := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(3, 3), Cents: 45000, KID: "000000000001"})
	desc := kidMatchDescription("000000000001", "Synthetic transfer")
	stranded, err := f.svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: f.refPeriod, EntryDate: dateIn(f.refYear, 3, 3), Description: desc, Source: "bank_import",
		SourceID: &strandedRow, SourceTable: strPtr("bank_import_rows"), CreatedBy: f.club.Treasurer.ID, ClubID: f.club.ID,
		Lines: []CreateJournalLineInput{{AccountCode: "1920", Debit: 450}, {AccountCode: "1500", Credit: 450}},
	})
	if err != nil {
		t.Fatalf("stranded draft: %v", err)
	}
	f.exec(t, `UPDATE bank_import_rows SET journal_entry_id = $1, auto_matched = true WHERE id = $2`, stranded.ID, strandedRow)
	f.exec(t, `UPDATE journal_entries SET created_at = now() - interval '1 hour' WHERE id = $1`, stranded.ID)

	vippsRow := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(3, 4), Cents: 30000})
	vippsDraft, err := f.svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: f.refPeriod, EntryDate: dateIn(f.refYear, 3, 4), Description: "Synthetic Vipps bilag", Source: "vipps",
		SourceID: &vippsRow, SourceTable: strPtr("bank_import_rows"), CreatedBy: f.club.Treasurer.ID, ClubID: f.club.ID,
		Lines: []CreateJournalLineInput{{AccountCode: "1920", Debit: 300}, {AccountCode: "3900", Credit: 300}},
	})
	if err != nil {
		t.Fatalf("vipps draft: %v", err)
	}
	f.exec(t, `UPDATE bank_import_rows SET journal_entry_id = $1 WHERE id = $2`, vippsDraft.ID, vippsRow)

	f.mark(t, true)

	if n, err := f.svc.healStrandedKIDDrafts(ctx, f.club.ID); err != nil || n != 0 {
		t.Errorf("healStrandedKIDDrafts released %d (%v) reference drafts, want 0", n, err)
	}
	if res, err := f.svc.ResyncVippsBilags(ctx, f.club.ID, f.club.Treasurer.ID); err != nil || res.Scanned != 0 {
		t.Errorf("ResyncVippsBilags scanned %+v (%v), want no reference candidate", res, err)
	}
	if res, err := f.svc.RegenerateOpenPeriodVippsReconciliations(ctx, f.club.ID, f.club.Treasurer.ID, true); err != nil || res.ScannedBilags != 0 {
		t.Errorf("cutover scanned %+v (%v), want no reference candidate", res, err)
	}
	for _, id := range []string{stranded.ID, vippsDraft.ID} {
		if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE id = $1 AND status = 'draft'`, id); n != 1 {
			t.Errorf("reference draft %s did not survive", id)
		}
	}
	if n := f.scalar(t, `SELECT count(*) FROM bank_import_rows WHERE journal_entry_id IS NOT NULL AND id = ANY($1::uuid[])`,
		[]string{strandedRow, vippsRow}); n != 2 {
		t.Errorf("reference rows were unlinked from their drafts")
	}

	// Unmarked, the same drafts are candidates, so the filter is the year's.
	f.mark(t, false)
	if n, err := f.svc.healStrandedKIDDrafts(ctx, f.club.ID); err != nil || n != 1 {
		t.Errorf("unmarked healStrandedKIDDrafts released %d (%v), want 1", n, err)
	}
	if res, err := f.svc.RegenerateOpenPeriodVippsReconciliations(ctx, f.club.ID, f.club.Treasurer.ID, true); err != nil || res.ScannedBilags != 1 {
		t.Errorf("unmarked cutover scanned %+v (%v), want 1", res, err)
	}
}

func strPtr(s string) *string { return &s }

// The backfills read posted 1500 credits and mark fakturas paid; a reference
// year's bookings must not do that.
func TestPaymentBackfillsSkipReferenceRows(t *testing.T) {
	f := newRefFixture(t, false)
	ctx := context.Background()
	imp := f.club.NewBankImport(t)
	member := f.club.NewMember(t, "member")
	byKID := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &member, Cents: 45000})
	byNumber := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &member, Cents: 52000})

	book := func(rowID string, day int, cents int64) {
		t.Helper()
		e, err := f.svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
			FiscalPeriodID: f.refPeriod, EntryDate: dateIn(f.refYear, 9, day), Description: "Synthetic innbetaling",
			Source: "bank_import", SourceID: &rowID, SourceTable: strPtr("bank_import_rows"),
			CreatedBy: f.club.Treasurer.ID, ClubID: f.club.ID,
			Lines: []CreateJournalLineInput{
				{AccountCode: "1920", Debit: float64(cents) / 100}, {AccountCode: "1500", Credit: float64(cents) / 100}},
		})
		if err != nil {
			t.Fatalf("entry: %v", err)
		}
		if err := f.svc.PostJournalEntry(ctx, f.club.ID, e.ID, f.club.Treasurer.ID); err != nil {
			t.Fatalf("post: %v", err)
		}
		f.exec(t, `UPDATE bank_import_rows SET journal_entry_id = $1 WHERE id = $2`, e.ID, rowID)
	}
	kidRow := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(9, 1), Cents: 45000, KID: byKID.KID})
	book(kidRow, 1, 45000)
	numRow := f.club.NewBankRow(t, imp, fin.BankRowOpts{Date: f.refDay(9, 2), Cents: 52000,
		Description: "Faktura " + strconv.Itoa(byNumber.Number)})
	book(numRow, 2, 52000)

	f.mark(t, true)
	if n, err := f.svc.backfillUnlinkedPayments(ctx, f.club.ID); err != nil || n != 0 {
		t.Errorf("KID backfill linked %d (%v) payments from a reference year, want 0", n, err)
	}
	if n, err := f.svc.backfillInvoiceNumberPayments(ctx, f.club.ID); err != nil || n != 0 {
		t.Errorf("invoice-number backfill linked %d (%v) payments from a reference year, want 0", n, err)
	}
	if n := f.scalar(t, `SELECT count(*) FROM invoices WHERE id = ANY($1::uuid[]) AND payment_id IS NULL`,
		[]string{byKID.ID, byNumber.ID}); n != 2 {
		t.Errorf("%d of 2 fakturas stayed unpaid, want both", n)
	}

	f.mark(t, false)
	if n, err := f.svc.backfillUnlinkedPayments(ctx, f.club.ID); err != nil || n != 1 {
		t.Errorf("unmarked KID backfill linked %d (%v), want 1", n, err)
	}
	if n, err := f.svc.backfillInvoiceNumberPayments(ctx, f.club.ID); err != nil || n != 1 {
		t.Errorf("unmarked invoice-number backfill linked %d (%v), want 1", n, err)
	}
}

// A Stripe payout whose deposit is dated in a reference year is left waiting
// with the reference code and books nothing, as a closed year would; the
// same payout in an ordinary year clears.
func TestStripePayoutClearingWaitsInReferenceYear(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	refYear := time.Now().Year() - 1
	period := addPeriod(t, f.club, refYear)
	day := func(month time.Month, d int) time.Time { return time.Date(refYear, month, d, 0, 0, 0, 0, time.UTC) }

	po := f.insertPayout(t, payoutSpec{id: "po_test_reference", amountOre: 11500, arrival: day(6, 10)})
	// A dismissed member counts as done, so only the year can hold the payout back.
	f.member(t, po, stripeBT{id: "txn_test_ref_member", category: "charge", amountOre: 11500,
		created: day(6, 8), status: StripeTxnDismissed})
	imp := f.club.NewBankImport(t)
	row := f.stripeDeposit(t, imp, day(6, 11), 11500, "STRIPE PAYMENTS PAYOUT")
	if _, _, err := f.svc.SetPeriodReference(ctx, f.club.ID, period, true); err != nil {
		t.Fatalf("mark: %v", err)
	}

	res := f.sweep(t)
	if len(res.Cleared) != 0 || len(res.Waiting) != 1 || !strings.Contains(res.Waiting[0].Reason, "referanseår") {
		t.Fatalf("sweep in a reference year = %+v, want one payout waiting with the reference reason", res)
	}
	var refusal *StripeClearRefusal
	if _, err := f.svc.ClearStripePayoutManually(ctx, f.club.ID, f.club.Treasurer.ID, po, row); !errors.As(err, &refusal) ||
		refusal.Code != StripeClearReferencePeriod {
		t.Errorf("manual clearing in a reference year: err = %v, want a %s refusal", err, StripeClearReferencePeriod)
	}
	if bankRowEntry(t, f.club, row) != "" || f.clearingEntries(t) != 0 {
		t.Errorf("a reference-year deposit was booked")
	}

	if _, _, err := f.svc.SetPeriodReference(ctx, f.club.ID, period, false); err != nil {
		t.Fatalf("unmark: %v", err)
	}
	if res := f.sweep(t); len(res.Cleared) != 1 {
		t.Errorf("sweep in an ordinary year = %+v, want the payout cleared", res)
	}
}
