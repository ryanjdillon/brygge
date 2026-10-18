package accounting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// A harbour-share buy-back paid out and the overpayment the recipient sent
// back two days later: synthetic stand-ins for the motivating case.
type buyBackPair struct {
	out, in string
}

func (f *hygieneFixture) buyBack(t *testing.T, importID string, day time.Time) buyBackPair {
	t.Helper()
	return buyBackPair{
		out: f.club.NewBankRow(t, importID, fin.BankRowOpts{
			Cents: -6300000, Date: day, Description: "Synthetic buy-back",
			Counterpart: "Kari Testdotter", ToAccount: "9999.00.11111",
		}),
		in: f.club.NewBankRow(t, importID, fin.BankRowOpts{
			Cents: 100000, Date: day.AddDate(0, 0, 2), Description: "Synthetic return",
			Counterpart: "TESTDOTTER KARI", FromAccount: "99990011111",
		}),
	}
}

func TestBankRowPartnersSuggestsBuyBackReturn(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()
	imp := f.club.NewBankImport(t)
	p := f.buyBack(t, imp, time.Date(f.club.Year, 8, 31, 0, 0, 0, 0, time.UTC))
	// An unrelated outgoing row in the same window is not suggested.
	f.club.NewBankRow(t, imp, fin.BankRowOpts{Cents: -39755, Date: time.Date(f.club.Year, 9, 1, 0, 0, 0, 0, time.UTC),
		Counterpart: "Synthetic Power Company"})

	got, err := f.svc.BankRowPartners(ctx, f.club.ID, p.in)
	if err != nil {
		t.Fatalf("partners: %v", err)
	}
	if len(got) != 1 || got[0].ID != p.out || got[0].Strength != PartnerStrong || got[0].Net != -62000 || got[0].DaysApart != 2 {
		t.Fatalf("partners = %+v, want the buy-back only, strong, net -62000, 2 days apart", got)
	}
	strong, err := f.svc.StrongBankRowPartners(ctx, f.club.ID, p.out)
	if err != nil || len(strong) != 1 || strong[0].ID != p.in {
		t.Fatalf("strong partners of the payout = %+v (%v), want the return", strong, err)
	}

	rows, err := f.svc.ListUnmatchedBankRows(ctx, f.club.ID, "", "", 0, 50, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	hints := map[string]string{}
	for _, r := range rows {
		if r.PartnerHint != nil {
			hints[r.ID] = *r.PartnerHint
		}
	}
	if len(hints) != 2 || hints[p.out] != PartnerStrong || hints[p.in] != PartnerStrong {
		t.Errorf("partner hints = %v, want strong on both rows of the pair only", hints)
	}

	// The hint does not depend on the page: the return alone on a page
	// still points at the payout.
	page, err := f.svc.ListUnmatchedBankRows(ctx, f.club.ID, "incoming", "", 0, 50, 0)
	if err != nil {
		t.Fatalf("list incoming: %v", err)
	}
	if len(page) != 1 || page[0].PartnerHint == nil || *page[0].PartnerHint != PartnerStrong {
		t.Errorf("incoming-only page lost the partner hint: %+v", page)
	}

	if _, err := f.svc.BankRowPartners(ctx, f.otherClub(t).ID, p.in); !errors.Is(err, ErrBankRowNotFound) {
		t.Errorf("another club's row: err = %v, want ErrBankRowNotFound", err)
	}
}

func TestBankRowPartnersSkipsBookedPartner(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()
	p := f.buyBack(t, f.club.NewBankImport(t), time.Date(f.club.Year, 8, 31, 0, 0, 0, 0, time.UTC))
	if _, err := f.svc.AssignBankRowToAccount(ctx, f.club.ID, f.club.Treasurer.ID, p.in, "3900", "revenue", ""); err != nil {
		t.Fatalf("book the return: %v", err)
	}
	strong, err := f.svc.StrongBankRowPartners(ctx, f.club.ID, p.out)
	if err != nil || len(strong) != 0 {
		t.Errorf("booked partner still suggested: %+v (%v)", strong, err)
	}
}

func TestAssignBankRowsToAccountGroupBooksNetAndUnassignsTogether(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()
	day := time.Date(f.club.Year, 8, 31, 0, 0, 0, 0, time.UTC)
	p := f.buyBack(t, f.club.NewBankImport(t), day)

	// A faktura paid the same day as the group entry for the net amount must
	// survive the group's unassign.
	member := f.club.NewMember(t)
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &member, Cents: 6200000, Paid: true})
	if _, err := f.club.Pool.Exec(ctx,
		`UPDATE payments SET paid_at = $1 WHERE id = (SELECT payment_id FROM invoices WHERE id = $2)`,
		day.AddDate(0, 0, 2), inv.ID,
	); err != nil {
		t.Fatalf("date payment: %v", err)
	}

	bankBefore := f.net(t, f.club, "1920")
	res, err := f.svc.AssignBankRowsToAccountGroup(ctx, f.club.ID, f.club.Treasurer.ID, []string{p.out, p.in}, "2050", "Innløysing av hamneandel")
	if err != nil {
		t.Fatalf("group assign: %v", err)
	}
	if res.Net != -62000 || len(res.RowIDs) != 2 {
		t.Errorf("result = %+v, want net -62000 over two rows", res)
	}
	if got := f.status(t, res.JournalEntryID); got != "posted" {
		t.Errorf("entry status = %s, want posted", got)
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_lines WHERE journal_entry_id = $1`, res.JournalEntryID); n != 2 {
		t.Errorf("%d lines, want 2 (bank net + account)", n)
	}
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE id = $1 AND source_id = $2
	                       AND entry_date = $3 AND description LIKE 'Samla tildeling:%'`,
		res.JournalEntryID, p.out, day.AddDate(0, 0, 2)); n != 1 {
		t.Errorf("entry not sourced to the larger row, dated on the latest row and described as a group")
	}
	if got := f.net(t, f.club, "1920"); got != bankBefore-62000 {
		t.Errorf("1920 net = %v, want %v", got, bankBefore-62000)
	}
	if got := f.net(t, f.club, "2050"); got != 62000 {
		t.Errorf("2050 net = %v, want 62000 debit", got)
	}
	if n := f.scalar(t, `SELECT count(*) FROM bank_import_rows WHERE id = ANY($1::uuid[]) AND journal_entry_id = $2`,
		[]string{p.out, p.in}, res.JournalEntryID); n != 2 {
		t.Errorf("%d of 2 rows linked to the entry", n)
	}

	un, err := f.svc.UnassignBankRowReported(ctx, f.club.ID, f.club.Treasurer.ID, p.in)
	if err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if len(un.SiblingRowIDs) != 1 || un.SiblingRowIDs[0] != p.out || un.PriorInvoiceID != "" {
		t.Errorf("unassign result = %+v, want the payout as sibling and no faktura", un)
	}
	if got := f.status(t, res.JournalEntryID); got != "voided" {
		t.Errorf("entry status after unassign = %s, want voided", got)
	}
	if n := f.scalar(t, `SELECT count(*) FROM bank_import_rows WHERE id = ANY($1::uuid[]) AND journal_entry_id IS NULL`,
		[]string{p.out, p.in}); n != 2 {
		t.Errorf("%d of 2 rows released", n)
	}
	if got := f.net(t, f.club, "1920"); got != bankBefore {
		t.Errorf("1920 net after unassign = %v, want %v", got, bankBefore)
	}
	if n := f.scalar(t, `SELECT count(*) FROM invoices WHERE id = $1 AND payment_id IS NOT NULL`, inv.ID); n != 1 {
		t.Errorf("same-day paid faktura lost its payment on group unassign")
	}
}

func TestAssignBankRowsToAccountGroupRefusals(t *testing.T) {
	f := newHygieneFixture(t)
	ctx := context.Background()
	actor := f.club.Treasurer.ID
	imp := f.club.NewBankImport(t)
	june := time.Date(f.club.Year, 6, 1, 0, 0, 0, 0, time.UTC)
	newRow := func(importID string, cents int64, day time.Time) string {
		return f.club.NewBankRow(t, importID, fin.BankRowOpts{Cents: cents, Date: day})
	}

	refusals := []struct {
		name    string
		ids     func() []string
		account string
		want    error
	}{
		{"net zero", func() []string { return []string{newRow(imp, -50000, june), newRow(imp, 50000, june)} }, "3900", ErrGroupNetZero},
		{"different bank accounts", func() []string {
			return []string{newRow(imp, -50000, june), newRow(f.club.NewBankImportOn(t, "1925"), 1000, june)}
		}, "3900", ErrGroupMixedBankAccounts},
		{"across a year-end", func() []string {
			return []string{
				newRow(imp, -50000, time.Date(f.club.Year, 12, 30, 0, 0, 0, 0, time.UTC)),
				newRow(imp, 1000, time.Date(f.club.Year+1, 1, 2, 0, 0, 0, 0, time.UTC)),
			}
		}, "3900", ErrGroupCrossesPeriod},
		{"a bank account as target", func() []string { return []string{newRow(imp, -50000, june), newRow(imp, 1000, june)} }, "1925", ErrGroupTargetAccount},
		{"receivables as target", func() []string { return []string{newRow(imp, -50000, june), newRow(imp, 1000, june)} }, "1500", ErrGroupTargetAccount},
		{"too few rows", func() []string { id := newRow(imp, -50000, june); return []string{id, id} }, "3900", ErrGroupRowCount},
		{"too many rows", func() []string {
			var ids []string
			for i := 0; i < 6; i++ {
				ids = append(ids, newRow(imp, int64(-1000-i), june))
			}
			return ids
		}, "3900", ErrGroupRowCount},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := f.svc.AssignBankRowsToAccountGroup(ctx, f.club.ID, actor, tt.ids(), tt.account, ""); !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
	t.Run("a closed period", func(t *testing.T) {
		ids := []string{newRow(imp, -50000, june), newRow(imp, 1000, june)}
		f.setPeriod(t, f.club, "closed")
		defer f.setPeriod(t, f.club, "open")
		if _, err := f.svc.AssignBankRowsToAccountGroup(ctx, f.club.ID, actor, ids, "3900", ""); !errors.Is(err, ErrGroupPeriodClosed) {
			t.Errorf("err = %v, want ErrGroupPeriodClosed", err)
		}
	})
	t.Run("a dismissed row", func(t *testing.T) {
		ids := []string{newRow(imp, -50000, june), newRow(imp, 1000, june)}
		if err := f.svc.DismissBankRow(ctx, f.club.ID, actor, ids[1], "unidentifiable"); err != nil {
			t.Fatalf("dismiss: %v", err)
		}
		if _, err := f.svc.AssignBankRowsToAccountGroup(ctx, f.club.ID, actor, ids, "3900", ""); !errors.Is(err, ErrGroupRowNotBookable) {
			t.Errorf("err = %v, want ErrGroupRowNotBookable", err)
		}
	})
	t.Run("an already-booked row links nothing", func(t *testing.T) {
		booked, free := newRow(imp, 1000, june), newRow(imp, -50000, june)
		if _, err := f.svc.AssignBankRowToAccount(ctx, f.club.ID, actor, booked, "3900", "revenue", ""); err != nil {
			t.Fatalf("book: %v", err)
		}
		if _, err := f.svc.AssignBankRowsToAccountGroup(ctx, f.club.ID, actor, []string{free, booked}, "2050", ""); !errors.Is(err, errBankRowRaced) {
			t.Errorf("err = %v, want errBankRowRaced", err)
		}
		if n := f.scalar(t, `SELECT count(*) FROM bank_import_rows WHERE id = $1 AND journal_entry_id IS NULL`, free); n != 1 {
			t.Errorf("the free row was linked")
		}
	})
	t.Run("another club's row", func(t *testing.T) {
		other := f.otherClub(t)
		foreign := other.NewBankRow(t, other.NewBankImport(t), fin.BankRowOpts{Cents: 1000, Date: june})
		mine := newRow(imp, -50000, june)
		if _, err := f.svc.AssignBankRowsToAccountGroup(ctx, f.club.ID, actor, []string{mine, foreign}, "2050", ""); !errors.Is(err, ErrBankRowNotFound) {
			t.Errorf("err = %v, want ErrBankRowNotFound", err)
		}
		if n := f.scalar(t, `SELECT count(*) FROM bank_import_rows WHERE id = ANY($1::uuid[]) AND journal_entry_id IS NULL`,
			[]string{mine, foreign}); n != 2 {
			t.Errorf("a row was linked")
		}
	})
	if n := f.scalar(t, `SELECT count(*) FROM journal_entries WHERE club_id = $1 AND description LIKE 'Samla tildeling:%'`, f.club.ID); n != 0 {
		t.Errorf("%d group entries left behind by refused bookings", n)
	}
}
