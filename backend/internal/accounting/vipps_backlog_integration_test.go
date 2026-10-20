package accounting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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
	res, err := f.svc.PostVippsDraftBacklog(context.Background(), f.club.ID, f.club.PeriodID, f.club.Treasurer.ID, apply, nil)
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
	draft := f.draftCharge(t, row, fallbackRevenueCode, 30000)

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
	if acct, _ := f.bookedAccount(t, row); acct != fallbackRevenueCode {
		t.Errorf("booked to %s, want %s", acct, fallbackRevenueCode)
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
	orphan := f.draftCharge(t, orphanRow, fallbackRevenueCode, 10000)
	f.exec(t, `UPDATE vipps_import_rows SET journal_entry_id = NULL WHERE id = $1`, orphanRow)

	// Unbalanced: a line edited after the fact.
	unbalRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 20000})
	unbal := f.draftCharge(t, unbalRow, fallbackRevenueCode, 20000)
	f.exec(t, `UPDATE journal_lines SET credit = 150 WHERE journal_entry_id = $1 AND credit > 0`, unbal)

	// Already booked: a posted entry exists for the same source row.
	dupRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 40000})
	dup := f.draftCharge(t, dupRow, fallbackRevenueCode, 40000)
	src := "vipps_import_rows"
	posted, err := f.svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: f.club.PeriodID, EntryDate: "2026-06-01", Description: "earlier booking",
		Source: "vipps", SourceID: &dupRow, SourceTable: &src, CreatedBy: f.club.Treasurer.ID, ClubID: f.club.ID,
		Lines: []CreateJournalLineInput{
			{AccountCode: vippsClearingAccountCode, Debit: 400},
			{AccountCode: fallbackRevenueCode, Credit: 400},
		},
	})
	if err != nil {
		t.Fatalf("earlier booking: %v", err)
	}
	if err := f.svc.PostJournalEntry(ctx, f.club.ID, posted.ID, f.club.Treasurer.ID); err != nil {
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
	if _, err := f.svc.PostVippsDraftBacklog(ctx, f.club.ID, f.club.PeriodID, f.club.Treasurer.ID, false, nil); err == nil {
		t.Errorf("a closed period must be refused")
	}
}

func TestVippsBacklogReclassifiesParkedCharges(t *testing.T) {
	f := newAutoAssignFixture(t) // auto-assign enabled
	f.priceItem(t, "service", "Slipping synthetic", 160000, "once", "non_member")

	// A 3900 fallback charge whose amount fits one price item.
	slipRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 160000, CustomerName: "Walk Up"})
	f.draftCharge(t, slipRow, fallbackRevenueCode, 160000)

	// A charge parked on 1500 with no recorded faktura, for a known member
	// with one matching open faktura and nothing marking a faktura paid by
	// it: the member_invoice rule assigns it to that faktura.
	payer := f.namedMember(t, "Parkedpayer")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &payer, Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	parkedRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Parkedpayer"})
	parked := f.draftCharge(t, parkedRow, receivablesAccountCode, 45000)

	// A charge parked on 1500 that no rule can place: refused, releasable.
	strayRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 77700, CustomerName: "Nobody Known"})
	stray := f.draftCharge(t, strayRow, receivablesAccountCode, 77700)

	preview := f.backlog(t, false)
	if len(preview.Reclassified) != 2 || preview.Posted != 0 || len(preview.Refused) != 1 ||
		len(preview.Releasable) != 1 || preview.Releasable[0].RowID != strayRow {
		t.Fatalf("preview = %+v, want 2 reclassified and the stray refused and releasable", preview)
	}
	if !strings.Contains(preview.Refused[0].Reason, "release it to the Vipps queue") {
		t.Errorf("stray refusal %q should point at the release action", preview.Refused[0].Reason)
	}

	applied := f.backlog(t, true)
	if len(applied.Reclassified) != 2 {
		t.Fatalf("apply = %+v", applied)
	}
	if acct, status := f.bookedAccount(t, slipRow); acct != "3300" || status != "posted" {
		t.Errorf("slipping charge booked to %s (%s), want 3300 posted", acct, status)
	}
	if !f.invoicePaid(t, inv.ID) || f.rowInvoice(t, parkedRow) != inv.ID {
		t.Errorf("parked charge not assigned to its member's open faktura")
	}
	if got := f.entryStatus(t, parked); got != "missing" {
		t.Errorf("parked draft is %s, want it deleted", got)
	}
	if got := f.entryStatus(t, stray); got != "draft" {
		t.Errorf("stray draft changed to %s, want it left as a draft", got)
	}
	if again := f.backlog(t, true); len(again.Reclassified) != 0 || again.Posted != 0 || len(again.Refused) != 1 {
		t.Errorf("second apply should only re-report the refusal: %+v", again)
	}
}

// An apply with an include-list rebooks only the ticked reclassifications.
// An unticked one is left exactly as it was — not rebooked, and not posted
// on its parked account either — and is offered again next time; an id that
// is not a reclassification now is reported, not acted on.
func TestVippsBacklogAppliesOnlyChosenReclassifications(t *testing.T) {
	f := newAutoAssignFixture(t)
	f.priceItem(t, "service", "Slipping synthetic", 160000, "once", "non_member")
	f.priceItem(t, "service", "Spyling synthetic", 50000, "once", "non_member")

	keepRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 160000, CustomerName: "Walk Up"})
	keep := f.draftCharge(t, keepRow, fallbackRevenueCode, 160000)
	skipRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 50000, CustomerName: "Walk Up"})
	skip := f.draftCharge(t, skipRow, fallbackRevenueCode, 50000)

	preview := f.backlog(t, false)
	if len(preview.Reclassified) != 2 {
		t.Fatalf("preview = %+v, want 2 reclassified", preview)
	}
	ids := map[string]string{}
	for _, r := range preview.Reclassified {
		ids[r.RowID] = r.EntryID
	}
	if ids[keepRow] != keep || ids[skipRow] != skip {
		t.Fatalf("reclassified ids = %v, want each row's draft", ids)
	}

	const tampered = "00000000-0000-0000-0000-000000000000"
	res, err := f.svc.PostVippsDraftBacklog(context.Background(), f.club.ID, f.club.PeriodID, f.club.Treasurer.ID, true,
		[]string{keep, tampered, tampered})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(res.Reclassified) != 1 || res.Reclassified[0].EntryID != keep || res.Unchecked != 1 || res.Posted != 0 {
		t.Fatalf("apply = %+v, want only the ticked draft rebooked and one unchecked", res)
	}
	if len(res.StaleIDs) != 1 || res.StaleIDs[0] != tampered {
		t.Errorf("stale ids = %v, want the tampered id once", res.StaleIDs)
	}
	if acct, status := f.bookedAccount(t, keepRow); acct != "3300" || status != "posted" {
		t.Errorf("ticked charge booked to %s (%s), want 3300 posted", acct, status)
	}
	if got := f.entryStatus(t, skip); got != "draft" {
		t.Errorf("unticked draft is %s, want it left as a draft", got)
	}
	if acct, _ := f.bookedAccount(t, skipRow); acct != fallbackRevenueCode {
		t.Errorf("unticked charge booked to %s, want it still on %s", acct, fallbackRevenueCode)
	}

	// The unticked draft is offered again; the handled one is now stale.
	again, err := f.svc.PostVippsDraftBacklog(context.Background(), f.club.ID, f.club.PeriodID, f.club.Treasurer.ID, false, []string{keep})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if again.Unchecked != 1 || len(again.StaleIDs) != 1 || again.StaleIDs[0] != keep {
		t.Errorf("re-preview = %+v, want the unticked draft unchecked and the booked one stale", again)
	}
	if all := f.backlog(t, false); len(all.Reclassified) != 1 || all.Reclassified[0].EntryID != skip || all.Unchecked != 0 {
		t.Errorf("preview without a list = %+v, want the unticked draft offered", all)
	}
}

// A 1500-parked charge whose amount fits a per-night price only as several
// nights is not rebooked: it is refused with the auto-assign reason and
// offered for release, like any charge no rule places.
func TestVippsBacklogLeavesMultiplesReleasable(t *testing.T) {
	f := newAutoAssignFixture(t)
	f.priceItem(t, "motorhome", "Synthetic motorhome spot", 30000, "day", "all")

	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 270000, CustomerName: "Synthetic Camper"})
	draft := f.draftCharge(t, row, receivablesAccountCode, 270000)

	res := f.backlog(t, true)
	if len(res.Reclassified) != 0 || res.Posted != 0 || len(res.Refused) != 1 ||
		len(res.Releasable) != 1 || res.Releasable[0].RowID != row {
		t.Fatalf("result = %+v, want the 2,700 refused and releasable", res)
	}
	if !strings.Contains(res.Refused[0].Reason, "amount only fits as several nights (9x Synthetic motorhome spot)") {
		t.Errorf("refusal %q should name the multiple", res.Refused[0].Reason)
	}
	if got := f.entryStatus(t, draft); got != "draft" {
		t.Errorf("draft changed to %s, want it left as a draft", got)
	}
}

// A legacy charge→faktura draft: the faktura was marked paid (a KID-avstemming
// payment) when the charge was assigned, but vipps_import_rows.invoice_id
// predates the column. It must be refused whether or not auto-assign is on,
// never rebooked as revenue (that would credit the money twice while the
// faktura stays paid), and never released to the queue.
func TestVippsBacklogRefusesLegacyFakturaDraft(t *testing.T) {
	for _, autoAssign := range []bool{true, false} {
		f := newAutoAssignFixture(t)
		f.exec(t, `UPDATE clubs SET vipps_auto_assign = $2 WHERE id = $1`, f.club.ID, autoAssign)
		f.priceItem(t, "membership", "Medlemskontingent synthetic", 45000, "once", "all")

		payer := f.namedMember(t, "Legacypayer")
		inv := f.legacyPaidInvoice(t, &payer, 45000)
		row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Legacypayer"})
		draft := f.draftCharge(t, row, receivablesAccountCode, 45000)

		res := f.backlog(t, true)
		if len(res.Reclassified) != 0 || res.Posted != 0 || len(res.Refused) != 1 || len(res.Releasable) != 0 ||
			!strings.Contains(res.Refused[0].Reason, fmt.Sprintf("faktura %d", inv.Number)) {
			t.Fatalf("auto-assign=%v: result = %+v, want the legacy draft refused naming faktura %d", autoAssign, res, inv.Number)
		}
		if _, err := f.svc.ReleaseVippsBacklogCharge(context.Background(), f.club.ID, f.club.Treasurer.ID, row); err == nil {
			t.Errorf("auto-assign=%v: release of a draft that may have paid a faktura must be refused", autoAssign)
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

// legacyPaidInvoice seeds a member's faktura marked paid the way a charge
// assignment did before vipps_import_rows.invoice_id existed: a
// KID-avstemming payment back-linked through payment_id, dated on the
// charge date, with no booking linked to the faktura.
func (f *autoAssignFixture) legacyPaidInvoice(t *testing.T, m *fin.Member, cents int64) fin.Invoice {
	t.Helper()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: m, Cents: cents, Paid: true,
		Lines: []fin.Line{{Category: "membership", Cents: cents}}})
	f.exec(t,
		`UPDATE payments SET description = 'KID-avstemming', paid_at = $2
		  WHERE id = (SELECT payment_id FROM invoices WHERE id = $1)`,
		inv.ID, time.Date(f.club.Year, 6, 1, 12, 0, 0, 0, time.UTC))
	return inv
}

func (f *autoAssignFixture) rowInvoice(t *testing.T, rowID string) string {
	t.Helper()
	var id *string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT invoice_id::text FROM vipps_import_rows WHERE id = $1`, rowID).Scan(&id); err != nil {
		t.Fatalf("row invoice: %v", err)
	}
	if id == nil {
		return ""
	}
	return *id
}

// rowEntryBalanced reports whether the row's booked entry balances.
func (f *autoAssignFixture) rowEntryBalanced(t *testing.T, rowID string) bool {
	t.Helper()
	var balanced bool
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT COALESCE((SELECT sum(debit) = sum(credit) AND sum(debit) > 0
		                    FROM journal_lines WHERE journal_entry_id = r.journal_entry_id), false)
		   FROM vipps_import_rows r WHERE r.id = $1`, rowID).Scan(&balanced); err != nil {
		t.Fatalf("row entry: %v", err)
	}
	return balanced
}

// A 1500-parked charge with no faktura that paid nothing is rebooked to the
// price item's account: the draft is deleted and a posted, balanced entry
// replaces it.
func TestVippsBacklogRebooksParkedChargeByPriceItem(t *testing.T) {
	f := newAutoAssignFixture(t)
	f.priceItem(t, "service", "Kranløft synthetic", 80000, "once", "all")
	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 80000, CustomerName: "Walk Up"})
	draft := f.draftCharge(t, row, receivablesAccountCode, 80000)

	preview := f.backlog(t, false)
	if len(preview.Reclassified) != 1 || preview.Reclassified[0].From != receivablesAccountCode ||
		preview.Reclassified[0].To != "3300" || len(preview.Refused) != 0 {
		t.Fatalf("preview = %+v, want 1500 → 3300", preview)
	}
	if got := f.entryStatus(t, draft); got != "draft" {
		t.Fatalf("preview changed the draft: %s", got)
	}

	applied := f.backlog(t, true)
	if len(applied.Reclassified) != 1 || len(applied.Refused) != 0 {
		t.Fatalf("apply = %+v", applied)
	}
	if got := f.entryStatus(t, draft); got != "missing" {
		t.Errorf("parked draft is %s, want it deleted", got)
	}
	if acct, status := f.bookedAccount(t, row); acct != "3300" || status != "posted" {
		t.Errorf("charge booked to %s (%s), want 3300 posted", acct, status)
	}
	if !f.rowEntryBalanced(t, row) {
		t.Errorf("rebooked entry does not balance")
	}
}

// A KID in the message naming an open faktura routes the parked charge to
// that faktura through the auto-assign decision.
func TestVippsBacklogAssignsParkedChargeToKIDFaktura(t *testing.T) {
	f := newAutoAssignFixture(t)
	owner := f.namedMember(t, "Kidowner")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &owner, Cents: 60000, Lines: []fin.Line{{Category: "membership", Cents: 60000}}})
	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 60000, CustomerName: "Someone Else Entirely"})
	f.exec(t, `UPDATE vipps_import_rows SET message = $2 WHERE id = $1`, row, "Kontingent "+inv.KID)
	draft := f.draftCharge(t, row, receivablesAccountCode, 60000)

	res := f.backlog(t, true)
	if len(res.Reclassified) != 1 || res.Reclassified[0].Rule != autoAssignRuleMemberInvoice ||
		res.Reclassified[0].To != fmt.Sprintf("faktura %d", inv.Number) {
		t.Fatalf("result = %+v, want the charge assigned to faktura %d", res, inv.Number)
	}
	if f.entryStatus(t, draft) != "missing" || f.rowInvoice(t, row) != inv.ID || !f.invoicePaid(t, inv.ID) {
		t.Errorf("charge not assigned to the KID faktura in place of its draft")
	}
	if !f.rowEntryBalanced(t, row) {
		t.Errorf("assigned entry does not balance")
	}
}

// No rule places the charge, or auto-assign is off: refused, offered for
// release, never posted as a receivable credit nothing clears.
func TestVippsBacklogRefusesUnplacedParkedCharge(t *testing.T) {
	f := newAutoAssignFixture(t)
	f.priceItem(t, "service", "Kranløft synthetic", 50000, "once", "all")
	f.priceItem(t, "guest", "Gjesteplass uke synthetic", 50000, "once", "all")
	ambiguous := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 50000, CustomerName: "Walk Up"})
	ambiguousDraft := f.draftCharge(t, ambiguous, receivablesAccountCode, 50000)

	res := f.backlog(t, true)
	if res.Posted != 0 || len(res.Reclassified) != 0 || len(res.Refused) != 1 || len(res.Releasable) != 1 ||
		!strings.Contains(res.Refused[0].Reason, "several categories") {
		t.Fatalf("ambiguous: result = %+v, want it refused with the reason and releasable", res)
	}
	if got := f.entryStatus(t, ambiguousDraft); got != "draft" {
		t.Errorf("ambiguous draft changed to %s", got)
	}

	f.exec(t, `UPDATE clubs SET vipps_auto_assign = false WHERE id = $1`, f.club.ID)
	f.priceItem(t, "service", "Slipping synthetic", 80000, "once", "all")
	fits := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 80000, CustomerName: "Walk Up"})
	fitsDraft := f.draftCharge(t, fits, receivablesAccountCode, 80000)

	off := f.backlog(t, true)
	if off.Posted != 0 || len(off.Reclassified) != 0 || len(off.Refused) != 2 || len(off.Releasable) != 2 {
		t.Fatalf("switch off: result = %+v, want both refused and releasable", off)
	}
	for _, r := range off.Refused {
		if !strings.Contains(r.Reason, "auto-assign is off") {
			t.Errorf("switch off: refusal %q should say auto-assign is off", r.Reason)
		}
	}
	if got := f.entryStatus(t, fitsDraft); got != "draft" {
		t.Errorf("switch off: draft changed to %s, want it left as a draft", got)
	}
}

// Releasing a charge that passes the test deletes its draft and puts the
// charge back in the Vipps queue.
func TestVippsBacklogReleaseParkedCharge(t *testing.T) {
	f := newAutoAssignFixture(t)
	ctx := context.Background()
	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 77700, CustomerName: "Nobody Known"})
	draft := f.draftCharge(t, row, receivablesAccountCode, 77700)

	prior, err := f.svc.ReleaseVippsBacklogCharge(ctx, f.club.ID, f.club.Treasurer.ID, row)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if prior != draft {
		t.Errorf("release returned %s, want the draft %s", prior, draft)
	}
	if got := f.entryStatus(t, draft); got != "missing" {
		t.Errorf("draft is %s after release, want it deleted", got)
	}
	if entry := f.rowEntry(t, row); entry != "" {
		t.Errorf("charge still booked to %s after release", entry)
	}
	queue, err := f.svc.ListUnreconciledVippsCharges(ctx, f.club.ID)
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if len(queue) != 1 {
		t.Errorf("queue has %d charges after release, want the released one", len(queue))
	}
	if _, err := f.svc.ReleaseVippsBacklogCharge(ctx, f.club.ID, f.club.Treasurer.ID, row); err == nil {
		t.Errorf("a second release of an unbooked charge must be refused")
	}
}

// Release refuses a locked period (a closed period cannot hold drafts) and
// any draft that is not a 1500 credit with no faktura.
func TestVippsBacklogReleaseRefusals(t *testing.T) {
	f := newAutoAssignFixture(t)
	ctx := context.Background()
	fallbackRow := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, CustomerName: "Walk Up"})
	f.draftCharge(t, fallbackRow, fallbackRevenueCode, 30000)
	if _, err := f.svc.ReleaseVippsBacklogCharge(ctx, f.club.ID, f.club.Treasurer.ID, fallbackRow); err == nil {
		t.Errorf("a 3900 draft must not be released")
	}

	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 77700, CustomerName: "Nobody Known"})
	draft := f.draftCharge(t, row, receivablesAccountCode, 77700)
	f.exec(t, `UPDATE fiscal_periods SET status = 'locked' WHERE id = $1`, f.club.PeriodID)
	if _, err := f.svc.ReleaseVippsBacklogCharge(ctx, f.club.ID, f.club.Treasurer.ID, row); !errors.Is(err, ErrPeriodClosed) {
		t.Errorf("release in a locked period: err = %v, want ErrPeriodClosed", err)
	}
	if got := f.entryStatus(t, draft); got != "draft" {
		t.Errorf("draft in a locked period changed to %s", got)
	}
	if _, err := f.svc.PostVippsDraftBacklog(ctx, f.club.ID, f.club.PeriodID, f.club.Treasurer.ID, true, nil); err == nil {
		t.Errorf("a backlog run in a locked period must be refused")
	}
}

// A payer that resolves to no member and carries no KID could have paid any
// faktura: an unexplained KID payment of the amount near the charge date
// blocks it, one a posted bank booking explains does not.
func TestVippsBacklogSettledTestForUnknownPayer(t *testing.T) {
	f := newAutoAssignFixture(t)
	ctx := context.Background()
	other := f.namedMember(t, "Otherpayer")
	inv := f.legacyPaidInvoice(t, &other, 45000)
	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Nobody Known"})
	f.draftCharge(t, row, receivablesAccountCode, 45000)

	res := f.backlog(t, false)
	if len(res.Refused) != 1 || len(res.Releasable) != 0 ||
		!strings.Contains(res.Refused[0].Reason, fmt.Sprintf("faktura %d", inv.Number)) {
		t.Fatalf("unexplained payment: result = %+v, want refused naming faktura %d", res, inv.Number)
	}

	// The faktura's payment is explained by a posted bank booking.
	src := "bank_import_rows"
	bankRow := f.club.NewBankRow(t, f.club.NewBankImport(t), fin.BankRowOpts{Cents: 45000})
	e, err := f.svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: f.club.PeriodID, EntryDate: "2026-06-01", Description: "bank KID payment",
		Source: "bank_import", SourceID: &bankRow, SourceTable: &src, CreatedBy: f.club.Treasurer.ID, ClubID: f.club.ID,
		Lines: []CreateJournalLineInput{
			{AccountCode: "1920", Debit: 450},
			{AccountCode: receivablesAccountCode, Credit: 450},
		},
	})
	if err != nil {
		t.Fatalf("bank booking: %v", err)
	}
	if err := f.svc.PostJournalEntry(ctx, f.club.ID, e.ID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("post bank booking: %v", err)
	}
	f.exec(t, `UPDATE bank_import_rows SET journal_entry_id = $2, invoice_id = $3 WHERE id = $1`, bankRow, e.ID, inv.ID)

	res = f.backlog(t, false)
	if len(res.Refused) != 1 || len(res.Releasable) != 1 || res.Releasable[0].RowID != row {
		t.Fatalf("explained payment: result = %+v, want the charge refused but releasable", res)
	}
}

// settledFaktura runs the settled-faktura test on the charge rowID (with no
// message) and returns the faktura that blocks it, or nil.
func (f *autoAssignFixture) settledFaktura(t *testing.T, rowID string) *settledFaktura {
	t.Helper()
	var customer string
	var amount float64
	var paidOn time.Time
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(customer_name, ''), amount, booking_date FROM vipps_import_rows WHERE id = $1`, rowID,
	).Scan(&customer, &amount, &paidOn); err != nil {
		t.Fatalf("row: %v", err)
	}
	s, err := f.svc.receivableChargeSettledFaktura(context.Background(), f.club.ID, customer, "", amount, paidOn)
	if err != nil {
		t.Fatalf("settled test: %v", err)
	}
	return s
}

// A draft (not only a posted entry) recorded against the faktura explains
// its payment, so another parked charge of the same amount passes.
func TestVippsBacklogSettledTestDraftExplains(t *testing.T) {
	f := newAutoAssignFixture(t)
	payer := f.namedMember(t, "Twodrafts")
	inv := f.legacyPaidInvoice(t, &payer, 45000)
	recorded := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Twodrafts"})
	f.draftCharge(t, recorded, receivablesAccountCode, 45000)
	f.exec(t, `UPDATE vipps_import_rows SET invoice_id = $1 WHERE id = $2`, inv.ID, recorded)
	parked := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Twodrafts"})
	f.draftCharge(t, parked, receivablesAccountCode, 45000)

	if s := f.settledFaktura(t, parked); s != nil {
		t.Fatalf("parked charge blocked by faktura %d, which the recorded draft explains", s.number)
	}
}

// The window applies whoever the payer is: a member who paid a household
// member's faktura is caught, and so is a faktura marked Vipps-paid.
func TestVippsBacklogSettledTestOtherMembersFaktura(t *testing.T) {
	f := newAutoAssignFixture(t)
	f.namedMember(t, "Payerp")
	spouse := f.namedMember(t, "Spouseq")
	inv := f.legacyPaidInvoice(t, &spouse, 45000)
	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Payerp"})
	f.draftCharge(t, row, receivablesAccountCode, 45000)

	res := f.backlog(t, false)
	if len(res.Refused) != 1 || len(res.Releasable) != 0 ||
		!strings.Contains(res.Refused[0].Reason, fmt.Sprintf("faktura %d", inv.Number)) {
		t.Fatalf("result = %+v, want refused naming the spouse's faktura %d", res, inv.Number)
	}

	g := newAutoAssignFixture(t)
	vippsPaid := g.club.NewInvoice(t, fin.InvoiceOpts{Cents: 33300, VippsPaid: true,
		Lines: []fin.Line{{Category: "membership", Cents: 33300}}})
	g.exec(t, `UPDATE invoices SET vipps_paid_at = $2 WHERE id = $1`,
		vippsPaid.ID, time.Date(g.club.Year, 6, 3, 12, 0, 0, 0, time.UTC))
	vRow := g.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 33300, CustomerName: "Nobody Known"})
	g.draftCharge(t, vRow, receivablesAccountCode, 33300)
	if s := g.settledFaktura(t, vRow); s == nil || s.number != vippsPaid.Number {
		t.Errorf("Vipps-paid faktura in the window: got %+v, want faktura %d", s, vippsPaid.Number)
	}
}

// The window is inclusive at 7 days from the charge date and ends there.
func TestVippsBacklogSettledTestWindowEdge(t *testing.T) {
	for _, tc := range []struct {
		day     int
		blocked bool
	}{{8, true}, {9, false}} {
		f := newAutoAssignFixture(t)
		other := f.namedMember(t, "Edgeowner")
		f.legacyPaidInvoice(t, &other, 45000)
		f.exec(t, `UPDATE payments SET paid_at = $2 WHERE club_id = $1`,
			f.club.ID, time.Date(f.club.Year, 6, tc.day, 12, 0, 0, 0, time.UTC))
		row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Nobody Known"})
		if got := f.settledFaktura(t, row) != nil; got != tc.blocked {
			t.Errorf("payment on June %d for a June 1 charge: blocked = %v, want %v", tc.day, got, tc.blocked)
		}
	}
}

// A plain unassign of a 1500 draft that may have paid a faktura is refused,
// not only a backlog release.
func TestUnassignRefusesLegacyPaidParkedDraft(t *testing.T) {
	f := newAutoAssignFixture(t)
	payer := f.namedMember(t, "Plainundo")
	inv := f.legacyPaidInvoice(t, &payer, 45000)
	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Synthetic Plainundo"})
	draft := f.draftCharge(t, row, receivablesAccountCode, 45000)

	_, _, err := f.svc.UnassignVippsCharge(context.Background(), f.club.ID, f.club.Treasurer.ID, row)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("faktura %d", inv.Number)) {
		t.Fatalf("unassign err = %v, want a refusal naming faktura %d", err, inv.Number)
	}
	if got := f.entryStatus(t, draft); got != "draft" {
		t.Errorf("draft changed to %s", got)
	}
	if !f.invoicePaid(t, inv.ID) {
		t.Errorf("faktura lost its payment")
	}
}
