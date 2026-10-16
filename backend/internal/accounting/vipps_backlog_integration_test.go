package accounting

import (
	"context"
	"strings"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// draftCharge books a charge the way the pre-BRY-339 code did: a draft
// DR clearing / CR account, linked from its row, never posted.
func (f *autoAssignFixture) draftCharge(t *testing.T, rowID, account string, cents int64) string {
	t.Helper()
	src := "vipps_import_rows"
	e, err := f.svc.CreateJournalEntry(context.Background(), CreateJournalEntryInput{
		FiscalPeriodID: f.club.PeriodID,
		EntryDate:      "2026-06-01",
		Description:    "Vipps innbetaling (legacy draft)",
		Source:         "vipps",
		SourceID:       &rowID,
		SourceTable:    &src,
		CreatedBy:      f.club.Treasurer.ID,
		ClubID:         f.club.ID,
		Lines: []CreateJournalLineInput{
			{AccountCode: vippsClearingAccountCode, Debit: fin.Kr(cents)},
			{AccountCode: account, Credit: fin.Kr(cents)},
		},
	})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	f.exec(t, `UPDATE vipps_import_rows SET journal_entry_id = $1 WHERE id = $2`, e.ID, rowID)
	return e.ID
}

func (f *autoAssignFixture) backlog(t *testing.T, apply bool) *VippsBacklogResult {
	t.Helper()
	res, err := f.svc.PostVippsDraftBacklog(context.Background(), f.club.ID, f.club.PeriodID, f.club.Treasurer.ID, apply)
	if err != nil {
		t.Fatalf("PostVippsDraftBacklog(apply=%v): %v", apply, err)
	}
	return res
}

func (f *autoAssignFixture) entryStatus(t *testing.T, id string) string {
	t.Helper()
	var s string
	if err := f.club.Pool.QueryRow(context.Background(), `SELECT status FROM journal_entries WHERE id = $1`, id).Scan(&s); err != nil {
		return "missing"
	}
	return s
}

func (f *autoAssignFixture) bookedAccount(t *testing.T, rowID string) (account, status string) {
	t.Helper()
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT a.code, je.status
		   FROM vipps_import_rows r
		   JOIN journal_entries je ON je.id = r.journal_entry_id
		   JOIN journal_lines jl ON jl.journal_entry_id = je.id AND jl.credit > 0
		   JOIN accounts a ON a.id = jl.account_id
		  WHERE r.id = $1`, rowID,
	).Scan(&account, &status); err != nil {
		t.Fatalf("booked account: %v", err)
	}
	return account, status
}

func TestVippsBacklogPreviewThenApply(t *testing.T) {
	f := newAutoAssignFixture(t)
	f.exec(t, `UPDATE clubs SET vipps_auto_assign = false WHERE id = $1`, f.club.ID)
	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, CustomerName: "Walk Up"})
	draft := f.draftCharge(t, row, vippsFallbackRevenueCode, 30000)

	preview := f.backlog(t, false)
	if preview.Applied || preview.Drafts != 1 || preview.Posted != 1 || len(preview.Refused) != 0 {
		t.Fatalf("preview = %+v", preview)
	}
	if got := f.entryStatus(t, draft); got != "draft" {
		t.Fatalf("preview changed the draft: %s", got)
	}

	applied := f.backlog(t, true)
	if applied.Posted != 1 {
		t.Fatalf("apply = %+v", applied)
	}
	if got := f.entryStatus(t, draft); got != "posted" {
		t.Errorf("draft status after apply = %s, want posted", got)
	}
	// Auto-assign off: the fallback charge is posted where it stands.
	if acct, _ := f.bookedAccount(t, row); acct != vippsFallbackRevenueCode {
		t.Errorf("booked to %s, want %s", acct, vippsFallbackRevenueCode)
	}
	if again := f.backlog(t, true); again.Drafts != 0 || again.Posted != 0 {
		t.Errorf("second apply should find nothing: %+v", again)
	}
}

func TestVippsBacklogRefusesUnsafeDrafts(t *testing.T) {
	f := newAutoAssignFixture(t)
	ctx := context.Background()

	// Orphan: no row links to it.
	orphanRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 10000})
	orphan := f.draftCharge(t, orphanRow, vippsFallbackRevenueCode, 10000)
	f.exec(t, `UPDATE vipps_import_rows SET journal_entry_id = NULL WHERE id = $1`, orphanRow)

	// Unbalanced: a line edited after the fact.
	unbalRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 20000})
	unbal := f.draftCharge(t, unbalRow, vippsFallbackRevenueCode, 20000)
	f.exec(t, `UPDATE journal_lines SET credit = 150 WHERE journal_entry_id = $1 AND credit > 0`, unbal)

	// Already booked: a posted entry exists for the same source row.
	dupRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 40000})
	dup := f.draftCharge(t, dupRow, vippsFallbackRevenueCode, 40000)
	src := "vipps_import_rows"
	posted, err := f.svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: f.club.PeriodID, EntryDate: "2026-06-01", Description: "earlier booking",
		Source: "vipps", SourceID: &dupRow, SourceTable: &src, CreatedBy: f.club.Treasurer.ID, ClubID: f.club.ID,
		Lines: []CreateJournalLineInput{
			{AccountCode: vippsClearingAccountCode, Debit: 400},
			{AccountCode: vippsFallbackRevenueCode, Credit: 400},
		},
	})
	if err != nil {
		t.Fatalf("earlier booking: %v", err)
	}
	if err := f.svc.PostJournalEntry(ctx, posted.ID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("post earlier booking: %v", err)
	}

	res := f.backlog(t, true)
	if res.Posted != 0 || len(res.Refused) != 3 {
		t.Fatalf("result = %+v, want 3 refused and nothing posted", res)
	}
	reasons := ""
	for _, r := range res.Refused {
		reasons += r.Reason + " | "
	}
	for _, want := range []string{"orphan", "unbalanced", "already booked by bilag"} {
		if !strings.Contains(reasons, want) {
			t.Errorf("refusals %q missing %q", reasons, want)
		}
	}
	for _, id := range []string{orphan, unbal, dup} {
		if got := f.entryStatus(t, id); got != "draft" {
			t.Errorf("refused draft %s was changed to %s", id, got)
		}
	}

	f.exec(t, `UPDATE fiscal_periods SET status = 'closed' WHERE id = $1`, f.club.PeriodID)
	if _, err := f.svc.PostVippsDraftBacklog(ctx, f.club.ID, f.club.PeriodID, f.club.Treasurer.ID, false); err == nil {
		t.Errorf("a closed period must be refused")
	}
}

func TestVippsBacklogReclassifiesParkedCharges(t *testing.T) {
	f := newAutoAssignFixture(t) // auto-assign enabled
	f.priceItem(t, "service", "Slipping synthetic", 160000, "once", "non_member")

	// A 3900 fallback charge whose amount fits one price item.
	slipRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 160000, CustomerName: "Walk Up"})
	f.draftCharge(t, slipRow, vippsFallbackRevenueCode, 160000)

	// A charge parked on 1500 for a known member with one matching faktura.
	payer := f.namedMember(t, "Parkedpayer")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &payer, Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	parkedRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Parkedpayer"})
	f.draftCharge(t, parkedRow, receivablesAccountCode, 45000)

	// A charge parked on 1500 that no rule can place.
	strayRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 77700, CustomerName: "Nobody Known"})
	stray := f.draftCharge(t, strayRow, receivablesAccountCode, 77700)

	preview := f.backlog(t, false)
	if len(preview.Reclassified) != 2 || preview.Posted != 0 || len(preview.Refused) != 1 {
		t.Fatalf("preview = %+v, want 2 reclassified and 1 refused", preview)
	}

	applied := f.backlog(t, true)
	if len(applied.Reclassified) != 2 {
		t.Fatalf("apply = %+v", applied)
	}
	if acct, status := f.bookedAccount(t, slipRow); acct != "3300" || status != "posted" {
		t.Errorf("slipping charge booked to %s (%s), want 3300 posted", acct, status)
	}
	if acct, status := f.bookedAccount(t, parkedRow); acct != receivablesAccountCode || status != "posted" {
		t.Errorf("parked charge booked to %s (%s), want 1500 posted", acct, status)
	}
	if !f.invoicePaid(t, inv.ID) {
		t.Errorf("faktura not settled by the reclassified charge")
	}
	if got := f.entryStatus(t, stray); got != "draft" {
		t.Errorf("unplaceable parked charge changed to %s, want it left as a draft", got)
	}
	if again := f.backlog(t, true); len(again.Reclassified) != 0 || again.Posted != 0 {
		t.Errorf("second apply should only re-report the refusal: %+v", again)
	}
}
