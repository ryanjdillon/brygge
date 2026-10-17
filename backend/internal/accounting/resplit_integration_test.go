package accounting

import (
	"context"
	"strings"
	"testing"
)

// bookMisSplit books an invoice the way the pre-split sync did: all revenue
// to 3100, regardless of the line categories.
func (f *syncFixture) bookMisSplit(t *testing.T, invoiceID string, amount float64) string {
	t.Helper()
	ctx := context.Background()
	sourceTable := "invoices"
	entry, err := f.svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: f.periodID,
		EntryDate:      "2026-03-01",
		Description:    "Faktura (legacy split)",
		Source:         "invoice_sync",
		SourceID:       &invoiceID,
		SourceTable:    &sourceTable,
		CreatedBy:      f.userID,
		ClubID:         f.clubID,
		Lines: []CreateJournalLineInput{
			{AccountCode: receivablesAccountCode, Debit: amount},
			{AccountCode: "3100", Credit: amount},
		},
	})
	if err != nil {
		t.Fatalf("legacy entry: %v", err)
	}
	if err := f.svc.PostJournalEntry(ctx, f.clubID, entry.ID, f.userID); err != nil {
		t.Fatalf("post legacy entry: %v", err)
	}
	return entry.ID
}

func (f *syncFixture) resplit(t *testing.T, apply bool) *ResplitResult {
	t.Helper()
	res, err := f.svc.ResplitInvoiceBilags(context.Background(), f.clubID, f.periodID, f.userID, apply)
	if err != nil {
		t.Fatalf("ResplitInvoiceBilags(apply=%v): %v", apply, err)
	}
	return res
}

func TestResplitCorrectsLegacySplitAndIsIdempotent(t *testing.T) {
	f := newSyncFixture(t)
	misSplit := f.invoice(t, 3001, 2500, "open", true, "")
	f.bookMisSplit(t, misSplit, 2500)

	// Booked by the current sync, so already split correctly.
	f.invoice(t, 3002, 2500, "open", true, "")
	if res := f.sync(t); res.Synced != 1 {
		t.Fatalf("sync of the correct invoice: %+v", res)
	}

	preview := f.resplit(t, false)
	if preview.Posted != 0 || len(preview.Invoices) != 1 || preview.Invoices[0].InvoiceNumber != 3001 {
		t.Fatalf("preview = %+v, want one planned correction for faktura 3001 and nothing posted", preview)
	}
	if got := f.net(t, "3100"); got != -2500 {
		t.Fatalf("preview changed the GL: 3100 net = %v, want -2500", got)
	}
	want := map[string]float64{"3100": -2500, "3120": 2500}
	for _, m := range preview.AccountTotals {
		if want[m.AccountCode] != m.Amount {
			t.Errorf("planned move %s = %v, want %v", m.AccountCode, m.Amount, want[m.AccountCode])
		}
	}

	applied := f.resplit(t, true)
	if applied.Posted != 1 {
		t.Fatalf("apply posted %d, want 1 (%+v)", applied.Posted, applied)
	}
	// Revenue moved off 3100 onto 3120; receivables untouched.
	if got := f.net(t, "3100"); got != 0 {
		t.Errorf("3100 net after resplit = %v, want 0", got)
	}
	if got := f.net(t, "3120"); got != -5000 {
		t.Errorf("3120 net after resplit = %v, want -5000 (two slip fakturas)", got)
	}
	if got := f.net(t, "1500"); got != 5000 {
		t.Errorf("1500 net = %v, want 5000 (unchanged)", got)
	}

	if again := f.resplit(t, true); again.Posted != 0 || len(again.Invoices) != 0 {
		t.Errorf("second apply should be a no-op: %+v", again)
	}
}

func TestResplitIgnoresVoidedAndFlagsTotalMismatch(t *testing.T) {
	f := newSyncFixture(t)
	ctx := context.Background()

	voided := f.invoice(t, 3101, 2500, "open", true, "")
	f.bookMisSplit(t, voided, 2500)
	if _, err := f.pool.Exec(ctx, `UPDATE invoices SET status = 'voided' WHERE id = $1`, voided); err != nil {
		t.Fatalf("void: %v", err)
	}
	if res := f.sync(t); res.Reversed != 1 {
		t.Fatalf("expected the voided invoice to be reversed: %+v", res)
	}

	// Booked at 2000 but the faktura now totals 2500: not a reclassification.
	changed := f.invoice(t, 3102, 2500, "open", true, "")
	f.bookMisSplit(t, changed, 2000)

	res := f.resplit(t, true)
	if res.Posted != 0 || len(res.Invoices) != 0 {
		t.Errorf("nothing should be posted: %+v", res)
	}
	if len(res.SkippedDetails) != 1 || res.SkippedDetails[0].Ref != "faktura 3102" ||
		!strings.Contains(res.SkippedDetails[0].Reason, "differs from the faktura total") {
		t.Errorf("skipped = %+v, want faktura 3102 flagged for a total mismatch", res.SkippedDetails)
	}
}
