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

	// A charge parked on 1500 for a known member with one matching open
	// faktura. Even though a rule could place it, a 1500 credit with no
	// recorded faktura may already have settled one, so it is refused.
	payer := f.namedMember(t, "Parkedpayer")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &payer, Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	parkedRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Parkedpayer"})
	parked := f.draftCharge(t, parkedRow, receivablesAccountCode, 45000)

	// A charge parked on 1500 that no rule can place.
	strayRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 77700, CustomerName: "Nobody Known"})
	stray := f.draftCharge(t, strayRow, receivablesAccountCode, 77700)

	preview := f.backlog(t, false)
	if len(preview.Reclassified) != 1 || preview.Posted != 0 || len(preview.Refused) != 2 {
		t.Fatalf("preview = %+v, want 1 reclassified and 2 refused", preview)
	}

	applied := f.backlog(t, true)
	if len(applied.Reclassified) != 1 {
		t.Fatalf("apply = %+v", applied)
	}
	if acct, status := f.bookedAccount(t, slipRow); acct != "3300" || status != "posted" {
		t.Errorf("slipping charge booked to %s (%s), want 3300 posted", acct, status)
	}
	if f.invoicePaid(t, inv.ID) {
		t.Errorf("faktura settled by a backlog run; a parked 1500 charge must be left for a human")
	}
	for _, id := range []string{parked, stray} {
		if got := f.entryStatus(t, id); got != "draft" {
			t.Errorf("parked receivable draft changed to %s, want it left as a draft", got)
		}
	}
	if again := f.backlog(t, true); len(again.Reclassified) != 0 || again.Posted != 0 || len(again.Refused) != 2 {
		t.Errorf("second apply should only re-report the refusals: %+v", again)
	}
}

// A legacy charge→faktura draft: the faktura was marked paid when the charge
// was assigned, but vipps_import_rows.invoice_id predates the column. It must
// be refused whether or not auto-assign is on, and never rebooked as revenue
// (that would credit the money twice while the faktura stays paid).
func TestVippsBacklogRefusesLegacyFakturaDraft(t *testing.T) {
	for _, autoAssign := range []bool{true, false} {
		f := newAutoAssignFixture(t)
		f.exec(t, `UPDATE clubs SET vipps_auto_assign = $2 WHERE id = $1`, f.club.ID, autoAssign)
		f.priceItem(t, "membership", "Medlemskontingent synthetic", 45000, "once", "all")

		payer := f.namedMember(t, "Legacypayer")
		inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &payer, Cents: 45000, Paid: true,
			Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
		row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Legacypayer"})
		draft := f.draftCharge(t, row, receivablesAccountCode, 45000)

		res := f.backlog(t, true)
		if len(res.Reclassified) != 0 || res.Posted != 0 || len(res.Refused) != 1 ||
			!strings.Contains(res.Refused[0].Reason, "no recorded faktura") {
			t.Fatalf("auto-assign=%v: result = %+v, want the legacy draft refused", autoAssign, res)
		}
		if got := f.entryStatus(t, draft); got != "draft" {
			t.Errorf("auto-assign=%v: legacy draft changed to %s", autoAssign, got)
		}
		if !f.invoicePaid(t, inv.ID) {
			t.Errorf("auto-assign=%v: faktura lost its payment", autoAssign)
		}
		var revenue int
		if err := f.club.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM journal_lines jl JOIN accounts a ON a.id = jl.account_id
			  JOIN journal_entries je ON je.id = jl.journal_entry_id
			 WHERE je.club_id = $1 AND a.code = '3100' AND jl.credit > 0 AND je.source = 'vipps'`,
			f.club.ID).Scan(&revenue); err != nil || revenue != 0 {
			t.Errorf("auto-assign=%v: charge credited to revenue (%d lines, err %v)", autoAssign, revenue, err)
		}
	}
}

// A charge with a recorded faktura posts as it stands, and several drafts
// for one faktura can't both settle it.
func TestVippsBacklogFakturaDraftsPostOnceEach(t *testing.T) {
	f := newAutoAssignFixture(t)
	payer := f.namedMember(t, "Twicepayer")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &payer, Cents: 45000, Paid: true,
		Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	first := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Twicepayer"})
	second := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Twicepayer"})
	d1 := f.draftCharge(t, first, receivablesAccountCode, 45000)
	d2 := f.draftCharge(t, second, receivablesAccountCode, 45000)
	f.exec(t, `UPDATE vipps_import_rows SET invoice_id = $1 WHERE id IN ($2, $3)`, inv.ID, first, second)

	res := f.backlog(t, true)
	if res.Posted != 1 || len(res.Refused) != 1 || !strings.Contains(res.Refused[0].Reason, "already settled") {
		t.Fatalf("result = %+v, want one posted and the second refused as already settled", res)
	}
	statuses := map[string]bool{f.entryStatus(t, d1): true, f.entryStatus(t, d2): true}
	if !statuses["posted"] || !statuses["draft"] {
		t.Errorf("drafts = %v, want one posted and one left as draft", statuses)
	}
	if again := f.backlog(t, true); again.Posted != 0 || len(again.Refused) != 1 {
		t.Errorf("re-run after the first apply = %+v, want a no-op plus the same refusal", again)
	}
}
