package accounting

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// fallbackFixture is a per-charge club with auto-assign on, so a settlement
// can be cleared and its charges reclassified afterwards.
type fallbackFixture struct {
	*autoAssignFixture
	sweep *payoutSweepFixture
}

func newFallbackFixture(t *testing.T) *fallbackFixture {
	t.Helper()
	sw := newPayoutSweepFixture(t)
	f := &fallbackFixture{autoAssignFixture: &autoAssignFixture{svc: sw.svc, club: sw.club}, sweep: sw}
	f.exec(t, `UPDATE clubs SET vipps_auto_assign = true WHERE id = $1`, f.club.ID)
	return f
}

// parked books a charge to the fallback account the way the legacy cascade
// and Bokfør Vipps-utkast left it: posted, DR 2900 / CR 3900.
func (f *fallbackFixture) parked(t *testing.T, cents int64, customer string) string {
	t.Helper()
	row := f.club.NewVippsRow(t, fin.VippsRowOpts{Cents: cents, CustomerName: customer})
	f.parkRow(t, row)
	return row
}

func (f *fallbackFixture) parkRow(t *testing.T, row string) {
	t.Helper()
	if _, err := f.svc.CreateVippsChargeBilag(context.Background(), f.club.ID, row, fallbackRevenueCode, "", f.club.Treasurer.ID); err != nil {
		t.Fatalf("park charge: %v", err)
	}
}

func (f *fallbackFixture) preview(t *testing.T) *VippsFallbackReclassResult {
	t.Helper()
	res, err := f.svc.PreviewVippsFallbackReclass(context.Background(), f.club.ID, "")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	return res
}

// targetsOf is what the UI sends: the proposed account per entry.
func targetsOf(res *VippsFallbackReclassResult) map[string]string {
	out := map[string]string{}
	for _, it := range res.Items {
		if it.Status == FallbackReclassReclassify {
			out[it.EntryID] = it.To
		}
	}
	return out
}

func (f *fallbackFixture) apply(t *testing.T, targets map[string]string) *VippsFallbackReclassResult {
	t.Helper()
	res, err := f.svc.ApplyVippsFallbackReclass(context.Background(), f.club.ID, f.club.Treasurer.ID, "", targets)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return res
}

func (f *fallbackFixture) item(t *testing.T, res *VippsFallbackReclassResult, rowID string) VippsFallbackReclassItem {
	t.Helper()
	for _, it := range res.Items {
		if it.RowID == rowID {
			return it
		}
	}
	t.Fatalf("charge %s not in result %+v", rowID, res.Items)
	return VippsFallbackReclassItem{}
}

func (f *fallbackFixture) entryCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID).Scan(&n); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	return n
}

// trialBalance is the net (debit minus credit) per account over posted and
// voided entries, which is what the reports sum.
func (f *fallbackFixture) trialBalance(t *testing.T) map[string]float64 {
	t.Helper()
	rows, err := f.club.Pool.Query(context.Background(),
		`SELECT a.code, COALESCE(sum(jl.debit) - sum(jl.credit), 0)
		   FROM journal_lines jl JOIN journal_entries je ON je.id = jl.journal_entry_id
		   JOIN accounts a ON a.id = jl.account_id
		  WHERE je.club_id = $1 AND je.status IN ('posted', 'voided')
		  GROUP BY a.code`, f.club.ID)
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var code string
		var net float64
		if err := rows.Scan(&code, &net); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[code] = net
	}
	return out
}

func (f *fallbackFixture) entryLines(t *testing.T, entryID string) map[string]float64 {
	t.Helper()
	lines, err := entryLineTotals(context.Background(), f.club.Pool, entryID)
	if err != nil {
		t.Fatalf("lines: %v", err)
	}
	out := map[string]float64{}
	for _, l := range lines {
		out[l.AccountCode] = l.Debit - l.Credit
	}
	return out
}

// A guest charge parked on 3900 inside a cleared settlement moves to the
// guest account; the payout's 2900 entries are untouched, the trial balance
// moves only from 3900 to 3200, and a second run does nothing.
func TestFallbackReclassMovesRevenueInClearedSettlement(t *testing.T) {
	f := newFallbackFixture(t)
	ctx := context.Background()
	f.priceItem(t, "guest", "Gjesteplass synthetic", 80000, "day", "all")
	// An amount two categories fit: ambiguous with no tie category.
	f.priceItem(t, "service", "Tjeneste synthetic", 50000, "once", "all")
	f.priceItem(t, "slip_fee", "Plassleie synthetic", 50000, "once", "all")

	s := f.sweep.settlement(t, "3000501", 2, []int64{80000, 50000, 12300}, 1500, true)
	guest, ambiguous, nothing := s.charges[0], s.charges[1], s.charges[2]
	for _, id := range s.charges {
		f.parkRow(t, id)
	}
	sweep := f.sweep.bankSync(t).VippsPayouts
	if len(sweep.Cleared) != 1 {
		t.Fatalf("settlement did not clear: %+v", sweep)
	}
	bankEntry := *f.sweep.bankEntry(t, s.bankRow)
	var feeEntry string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT id::text FROM journal_entries WHERE club_id = $1 AND description LIKE 'Vipps gebyr oppgjør%'`,
		f.club.ID).Scan(&feeEntry); err != nil {
		t.Fatalf("fee entry: %v", err)
	}
	bankLines, feeLines := f.entryLines(t, bankEntry), f.entryLines(t, feeEntry)
	before := f.trialBalance(t)
	guestOld := f.rowEntry(t, guest)
	entriesBefore := f.entryCount(t)

	preview := f.preview(t)
	if preview.Applied || preview.Proposed != 1 || preview.Stays != 2 || preview.NeedsTildel != 0 {
		t.Fatalf("preview = %+v, want 1 proposed and 2 staying", preview)
	}
	if len(preview.Totals) != 1 || preview.Totals[0].AccountCode != "3200" || preview.Totals[0].Count != 1 || preview.Totals[0].Amount != 800 {
		t.Errorf("totals = %+v, want 3200: 1 charge, 800 kr", preview.Totals)
	}
	g := f.item(t, preview, guest)
	if g.To != "3200" || g.PriceItem != "Gjesteplass synthetic" || g.From != "3900" || g.Amount != 800 {
		t.Errorf("guest item = %+v", g)
	}
	if a := f.item(t, preview, ambiguous); a.Status != FallbackReclassStays || a.ReasonCode != decideCodeSeveralCategories ||
		!strings.Contains(a.Reason, "several categories") {
		t.Errorf("ambiguous item = %+v, want it to stay with the several-categories reason", a)
	}
	if n := f.item(t, preview, nothing); n.Status != FallbackReclassStays || n.ReasonCode != decideCodeNoMatch {
		t.Errorf("no-rule item = %+v", n)
	}
	if f.entryCount(t) != entriesBefore || f.rowEntry(t, guest) != guestOld {
		t.Fatalf("preview changed the books")
	}

	applied := f.apply(t, targetsOf(preview))
	if !applied.Applied || applied.Reclassified != 1 || applied.Failed != 0 || applied.Skipped != 0 {
		t.Fatalf("apply = %+v", applied)
	}
	moved := f.item(t, applied, guest)
	if moved.Outcome != FallbackOutcomeReclassified || moved.NewEntryNumber == 0 || moved.ReversalEntryNumber == 0 {
		t.Errorf("guest outcome = %+v", moved)
	}

	// The charge is relinked to a posted replacement DR 2900 / CR 3200; the
	// original is voided and its reversal posted.
	repl := f.rowEntry(t, guest)
	if repl == guestOld || f.entryStatus(t, repl) != "posted" || f.entryStatus(t, guestOld) != "voided" {
		t.Fatalf("replacement %s (%s), original %s (%s)", repl, f.entryStatus(t, repl), guestOld, f.entryStatus(t, guestOld))
	}
	if l := f.entryLines(t, repl); len(l) != 2 || l[vippsClearingAccountCode] != 800 || l["3200"] != -800 {
		t.Errorf("replacement lines = %v, want DR 2900 / CR 3200 for 800", l)
	}
	var desc string
	var rule *string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT je.description, r.auto_assign_rule FROM journal_entries je
		   JOIN vipps_import_rows r ON r.journal_entry_id = je.id WHERE r.id = $1`, guest).Scan(&desc, &rule); err != nil {
		t.Fatalf("replacement: %v", err)
	}
	if !strings.Contains(desc, "Reklassifisert fra 3900 (bilag #") || !strings.Contains(desc, "Gjesteplass synthetic") {
		t.Errorf("replacement description %q does not name the voided bilag and the price item", desc)
	}
	if rule == nil || *rule != autoAssignRulePriceItem {
		t.Errorf("auto_assign_rule = %v, want %s", rule, autoAssignRulePriceItem)
	}

	// The charges that stayed are untouched.
	for _, id := range []string{ambiguous, nothing} {
		if acct, status := f.bookedAccount(t, id); acct != "3900" || status != "posted" {
			t.Errorf("charge %s now on %s (%s), want 3900 posted", id, acct, status)
		}
	}
	// The payout's clearing entries are untouched.
	for id, want := range map[string]map[string]float64{bankEntry: bankLines, feeEntry: feeLines} {
		if f.entryStatus(t, id) != "posted" {
			t.Errorf("clearing entry %s is %s", id, f.entryStatus(t, id))
		}
		got := f.entryLines(t, id)
		for code, v := range want {
			if got[code] != v {
				t.Errorf("clearing entry %s line %s = %v, want %v", id, code, got[code], v)
			}
		}
	}
	// Trial balance: only 3900 -> 3200 moved, by the charge amount.
	after := f.trialBalance(t)
	for code, v := range before {
		want := v
		switch code {
		case "3900":
			want = v + 800
		}
		if after[code] != want {
			t.Errorf("account %s net = %v, want %v", code, after[code], want)
		}
	}
	if after["3200"] != -800 {
		t.Errorf("3200 net = %v, want -800", after["3200"])
	}
	if after[vippsClearingAccountCode] != 0 {
		t.Errorf("2900 net = %v, the cleared settlement should still net to 0", after[vippsClearingAccountCode])
	}

	// Idempotent: the same request again finds nothing to do.
	entriesAfter := f.entryCount(t)
	again := f.apply(t, targetsOf(preview))
	if again.Reclassified != 0 || len(again.StaleIDs) != 1 || f.entryCount(t) != entriesAfter {
		t.Errorf("second apply = %+v (entries %d -> %d), want nothing done", again, entriesAfter, f.entryCount(t))
	}
	if again := f.preview(t); again.Proposed != 0 || again.Stays != 2 {
		t.Errorf("preview after = %+v, want no proposal", again)
	}

	// The replacement is the charge's booking now, so Angre treats it like
	// any charge booking: refused while its settlement is cleared, allowed
	// once the deposit is undone.
	if _, _, err := f.svc.UnassignVippsCharge(ctx, f.club.ID, f.club.Treasurer.ID, guest); !errors.Is(err, ErrVippsChargeInClearedSettlement) {
		t.Fatalf("unassign the replacement in a cleared settlement = %v, want ErrVippsChargeInClearedSettlement", err)
	}
	if f.rowEntry(t, guest) != repl {
		t.Fatalf("a refused unassign moved the charge")
	}
	if _, _, err := f.svc.UnassignBankRow(ctx, f.club.ID, f.club.Treasurer.ID, s.bankRow); err != nil {
		t.Fatalf("undo the deposit: %v", err)
	}
	if _, _, err := f.svc.UnassignVippsCharge(ctx, f.club.ID, f.club.Treasurer.ID, guest); err != nil {
		t.Fatalf("unassign the replacement after the deposit was undone: %v", err)
	}
	if f.entryStatus(t, repl) != "voided" || f.rowEntry(t, guest) != "" {
		t.Errorf("unassign left the charge on %s (%s)", f.rowEntry(t, guest), f.entryStatus(t, repl))
	}
}

// A charge whose payer has one matching open faktura is not reclassified
// here: a reclass is revenue-only, so it is listed for Tildel with the
// faktura number.
func TestFallbackReclassListsFakturaMatchAsNeedsTildel(t *testing.T) {
	f := newFallbackFixture(t)
	f.priceItem(t, "guest", "Gjesteplass synthetic", 45000, "day", "all")
	payer := f.namedMember(t, "Tildelpayer")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &payer, Cents: 45000, Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	row := f.parked(t, 45000, "Synthetic Tildelpayer")

	preview := f.preview(t)
	it := f.item(t, preview, row)
	if it.Status != FallbackReclassNeedsTildel || it.InvoiceNumber != inv.Number || preview.NeedsTildel != 1 || preview.Proposed != 0 {
		t.Fatalf("item = %+v, preview = %+v, want needs_tildel with faktura %d", it, preview, inv.Number)
	}
	before := f.entryCount(t)
	applied := f.apply(t, map[string]string{it.EntryID: "3200"})
	if applied.Reclassified != 0 || len(applied.StaleIDs) != 1 || f.entryCount(t) != before {
		t.Errorf("apply = %+v, want the needs-Tildel charge left alone", applied)
	}
	if acct, _ := f.bookedAccount(t, row); acct != "3900" {
		t.Errorf("charge moved to %s", acct)
	}
}

// A faktura marked paid for the charge's amount that no booking explains may
// be this charge; reclassifying it would bury a double count.
func TestFallbackReclassKeepsChargeThatMayHavePaidAFaktura(t *testing.T) {
	f := newFallbackFixture(t)
	f.priceItem(t, "guest", "Gjesteplass synthetic", 80000, "day", "all")
	payer := f.namedMember(t, "Paidpayer")
	f.legacyPaidInvoice(t, &payer, 80000)
	row := f.parked(t, 80000, "Somebody Else")

	it := f.item(t, f.preview(t), row)
	if it.Status != FallbackReclassStays || it.ReasonCode != reasonCodeMaybePaid {
		t.Fatalf("item = %+v, want it to stay as a possible faktura payment", it)
	}
}

// Shapes this action does not own stay put with a reason: a charge that is
// recorded against a faktura or whose bilag has more lines.
func TestFallbackReclassLeavesOtherShapesAlone(t *testing.T) {
	f := newFallbackFixture(t)
	f.priceItem(t, "guest", "Gjesteplass synthetic", 80000, "day", "all")

	split := f.parked(t, 80000, "Split Payer")
	f.exec(t,
		`INSERT INTO journal_lines (journal_entry_id, account_id, debit, credit)
		 SELECT r.journal_entry_id, a.id, 10, 0 FROM vipps_import_rows r, accounts a WHERE r.id = $1 AND a.club_id = $2 AND a.code = '1930'`,
		split, f.club.ID)
	f.exec(t,
		`INSERT INTO journal_lines (journal_entry_id, account_id, debit, credit)
		 SELECT r.journal_entry_id, a.id, 0, 10 FROM vipps_import_rows r, accounts a WHERE r.id = $1 AND a.club_id = $2 AND a.code = '3300'`,
		split, f.club.ID)
	withInv := f.parked(t, 80000, "Faktura Payer")
	payer := f.namedMember(t, "Recorded")
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &payer, Cents: 99900, Lines: []fin.Line{{Category: "membership", Cents: 99900}}})
	f.exec(t, `UPDATE vipps_import_rows SET invoice_id = $2 WHERE id = $1`, withInv, inv.ID)

	preview := f.preview(t)
	if it := f.item(t, preview, split); it.ReasonCode != reasonCodeShape {
		t.Errorf("multi-line bilag item = %+v, want %s", it, reasonCodeShape)
	}
	if it := f.item(t, preview, withInv); it.ReasonCode != reasonCodeHasFaktura {
		t.Errorf("faktura-linked item = %+v, want %s", it, reasonCodeHasFaktura)
	}
	if preview.Proposed != 0 {
		t.Errorf("proposed = %d, want 0", preview.Proposed)
	}
}

// A closed or locked period is refused: the charge is not listed (counted as
// untouched), asking for the period by id fails, and a period closed between
// the plan and the transaction rolls the charge back whole.
func TestFallbackReclassRefusesClosedAndLockedPeriods(t *testing.T) {
	for _, status := range []string{"closed", "locked"} {
		t.Run(status, func(t *testing.T) {
			f := newFallbackFixture(t)
			ctx := context.Background()
			f.priceItem(t, "guest", "Gjesteplass synthetic", 80000, "day", "all")
			row := f.parked(t, 80000, "Closed Payer")
			plan := f.preview(t)
			it := f.item(t, plan, row)
			c := &fallbackCharge{entryID: it.EntryID, rowID: row}
			c.entryNumber = it.EntryNumber

			f.exec(t, `UPDATE fiscal_periods SET status = $2::fiscal_period_status WHERE id = $1`, f.club.PeriodID, status)
			entries := f.entryCount(t)

			if _, err := f.svc.PreviewVippsFallbackReclass(ctx, f.club.ID, f.club.PeriodID); !errors.Is(err, ErrPeriodClosed) {
				t.Errorf("preview of a %s period = %v, want ErrPeriodClosed", status, err)
			}
			res := f.preview(t)
			if len(res.Items) != 0 || res.ClosedPeriodCharges != 1 || res.ClosedPeriodAmount != 800 {
				t.Errorf("preview = %+v, want the charge counted as in a closed period", res)
			}
			if applied := f.apply(t, map[string]string{it.EntryID: "3200"}); applied.Reclassified != 0 || len(applied.StaleIDs) != 1 {
				t.Errorf("apply = %+v", applied)
			}
			if f.entryCount(t) != entries || f.rowEntry(t, row) != it.EntryID {
				t.Fatalf("a %s period was changed", status)
			}

			// The transaction itself re-checks the period under its lock.
			lines, err := entryLineTotals(ctx, f.club.Pool, it.EntryID)
			if err != nil {
				t.Fatal(err)
			}
			c.lines, c.amount = lines, 800
			_, err = f.svc.reclassifyFallbackCharge(ctx, f.club.ID, f.club.Treasurer.ID, vippsClearingAccountCode, c, "3200", "x")
			if !errors.Is(err, ErrPeriodClosed) {
				t.Errorf("reclassify in a %s period = %v, want ErrPeriodClosed", status, err)
			}
			if f.entryCount(t) != entries || f.entryStatus(t, it.EntryID) != "posted" {
				t.Errorf("a refused reclass left a partial change")
			}
		})
	}
}

// With auto-assign off the preview says why each charge stays and the apply
// refuses everything.
func TestFallbackReclassRefusedWhileAutoAssignIsOff(t *testing.T) {
	f := newFallbackFixture(t)
	f.priceItem(t, "guest", "Gjesteplass synthetic", 80000, "day", "all")
	row := f.parked(t, 80000, "Switch Payer")
	f.exec(t, `UPDATE clubs SET vipps_auto_assign = false WHERE id = $1`, f.club.ID)

	preview := f.preview(t)
	it := f.item(t, preview, row)
	if preview.AutoAssignEnabled || preview.Proposed != 0 || it.ReasonCode != reasonCodeAutoAssignOff {
		t.Fatalf("preview = %+v, item = %+v", preview, it)
	}
	before := f.entryCount(t)
	_, err := f.svc.ApplyVippsFallbackReclass(context.Background(), f.club.ID, f.club.Treasurer.ID, "", map[string]string{it.EntryID: "3200"})
	if !errors.Is(err, ErrVippsAutoAssignOff) {
		t.Fatalf("apply = %v, want ErrVippsAutoAssignOff", err)
	}
	if f.entryCount(t) != before {
		t.Errorf("a refused apply changed the books")
	}
}

// Only the previewed target is applied: a different account, or an entry the
// request does not name, is left alone.
func TestFallbackReclassAppliesOnlyWhatWasPreviewed(t *testing.T) {
	f := newFallbackFixture(t)
	f.priceItem(t, "guest", "Gjesteplass synthetic", 80000, "day", "all")
	listed := f.parked(t, 80000, "Listed Payer")
	unlisted := f.parked(t, 80000, "Unlisted Payer")
	wrong := f.parked(t, 80000, "Wrong Target Payer")
	preview := f.preview(t)
	li, wr := f.item(t, preview, listed), f.item(t, preview, wrong)

	applied := f.apply(t, map[string]string{li.EntryID: "3200", wr.EntryID: "3100"})
	if applied.Reclassified != 1 || applied.Skipped != 2 {
		t.Fatalf("apply = %+v, want 1 moved and 2 skipped", applied)
	}
	if acct, _ := f.bookedAccount(t, listed); acct != "3200" {
		t.Errorf("listed charge on %s, want 3200", acct)
	}
	for _, id := range []string{unlisted, wrong} {
		if acct, _ := f.bookedAccount(t, id); acct != "3900" {
			t.Errorf("charge %s moved to %s although it was not applied", id, acct)
		}
	}
}

// Two applies at once reclassify each charge once: the loser waits on the
// charge row, then sees it already moved.
func TestFallbackReclassConcurrentApplyRepostsOnce(t *testing.T) {
	f := newFallbackFixture(t)
	f.priceItem(t, "guest", "Gjesteplass synthetic", 80000, "day", "all")
	var rows []string
	for i := 0; i < 4; i++ {
		rows = append(rows, f.parked(t, 80000, "Racer Payer"))
	}
	targets := targetsOf(f.preview(t))
	if len(targets) != 4 {
		t.Fatalf("targets = %v", targets)
	}
	entriesBefore := f.entryCount(t)

	const runners = 2
	results := make([]*VippsFallbackReclassResult, runners)
	errs := make([]error, runners)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < runners; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = f.svc.ApplyVippsFallbackReclass(context.Background(), f.club.ID, f.club.Treasurer.ID, "", targets)
		}(i)
	}
	close(start)
	wg.Wait()

	moved := 0
	for i, r := range results {
		if errs[i] != nil {
			t.Fatalf("runner %d: %v", i, errs[i])
		}
		moved += r.Reclassified
		if r.Failed != 0 {
			t.Errorf("runner %d failed items: %+v", i, r.Items)
		}
	}
	if moved != 4 {
		t.Errorf("reclassified %d in total, want exactly 4 (once each)", moved)
	}
	// Per charge: one reversal and one replacement, so 2 new entries each.
	if got := f.entryCount(t) - entriesBefore; got != 8 {
		t.Errorf("%d new entries, want 8", got)
	}
	for _, id := range rows {
		if acct, status := f.bookedAccount(t, id); acct != "3200" || status != "posted" {
			t.Errorf("charge %s on %s (%s)", id, acct, status)
		}
	}
	if got := f.net(t, "3200"); got != -3200 {
		t.Errorf("3200 net = %v, want -3200", got)
	}
}
