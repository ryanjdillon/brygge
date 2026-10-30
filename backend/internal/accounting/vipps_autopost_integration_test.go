package accounting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

func newAutopostClub(t *testing.T) (*Service, *fin.Club) {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := NewService(pool, nil, zerolog.Nop())
	return svc, fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
}

func entryStatus(t *testing.T, svc *Service, entryID string) string {
	t.Helper()
	var status string
	if err := svc.db.QueryRow(context.Background(),
		`SELECT status FROM journal_entries WHERE id = $1`, entryID,
	).Scan(&status); err != nil {
		t.Fatalf("read entry %s: %v", entryID, err)
	}
	return status
}

func clubNet(t *testing.T, svc *Service, club *fin.Club, code string) float64 {
	t.Helper()
	tb, err := svc.TrialBalance(context.Background(), club.ID, club.PeriodID, false)
	if err != nil {
		t.Fatalf("TrialBalance: %v", err)
	}
	for _, l := range tb.Lines {
		if l.AccountCode == code {
			return l.Debit - l.Credit
		}
	}
	return 0
}

// Every Vipps posting path books a posted entry, so the reports see Vipps
// income, receivable settlements, fees and payouts without a manual post.
func TestVippsPostingsArePostedOnCreation(t *testing.T) {
	svc, club := newAutopostClub(t)
	ctx := context.Background()
	booking := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	const msn = "700001"

	member := club.NewMember(t, "member")
	inv := club.NewInvoice(t, fin.InvoiceOpts{Member: &member, Cents: 50000})
	toInvoice := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 50000, Date: booking, MSN: msn, CustomerName: "Synthetic Payer"})
	toAccount := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, Date: booking, MSN: msn, CustomerName: "Synthetic Guest"})
	club.NewVippsRow(t, fin.VippsRowOpts{RowType: "fee", Cents: -1000, Date: booking, MSN: msn})
	club.NewVippsRow(t, fin.VippsRowOpts{RowType: "payout", Cents: -79000, Date: booking, MSN: msn, Settlement: "2000777"})
	bankRow := club.NewBankRow(t, club.NewBankImport(t), fin.BankRowOpts{
		Date: booking.AddDate(0, 0, 2), Cents: 79000, Description: "Utb. 2000777 Vippsnr " + msn,
	})

	assigned, err := svc.AssignVippsChargeToInvoice(ctx, club.ID, toInvoice, inv.ID, club.Treasurer.ID)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if got := entryStatus(t, svc, assigned.EntryID); got != "posted" {
		t.Errorf("charge→faktura entry status = %q, want posted", got)
	}
	bilag, err := svc.CreateVippsChargeBilag(ctx, club.ID, toAccount, "3200", "", club.Treasurer.ID)
	if err != nil {
		t.Fatalf("bilag: %v", err)
	}
	if got := entryStatus(t, svc, bilag.EntryID); got != "posted" {
		t.Errorf("charge→account entry status = %q, want posted", got)
	}

	clear, err := svc.ClearVippsPayout(ctx, club.ID, bankRow, club.Treasurer.ID)
	if err != nil {
		t.Fatalf("clear payout: %v", err)
	}
	if !clear.Ready || clear.FeeEntryID == "" || clear.BankEntryID == "" {
		t.Fatalf("clear = %+v, want ready with fee and bank entries", clear)
	}
	for name, id := range map[string]string{"fee": clear.FeeEntryID, "payout": clear.BankEntryID} {
		if got := entryStatus(t, svc, id); got != "posted" {
			t.Errorf("%s entry status = %q, want posted", name, got)
		}
	}
	if !floatNear(clear.ClearingBalance, 0, 0.01) {
		t.Errorf("clearing balance = %v, want 0", clear.ClearingBalance)
	}

	var drafts int
	if err := svc.db.QueryRow(ctx,
		`SELECT count(*) FROM journal_entries WHERE club_id = $1 AND status = 'draft'`, club.ID,
	).Scan(&drafts); err != nil || drafts != 0 {
		t.Errorf("drafts left = %d (err %v), want 0", drafts, err)
	}

	// The reports now carry the Vipps side of the books.
	if got := clubNet(t, svc, club, "3200"); got != -300 {
		t.Errorf("3200 net = %v, want -300 (walk-up income)", got)
	}
	if got := clubNet(t, svc, club, "7700"); got != 10 {
		t.Errorf("7700 net = %v, want 10 (Vipps fee)", got)
	}
	if got := clubNet(t, svc, club, "1920"); got != 790 {
		t.Errorf("1920 net = %v, want 790 (net payout)", got)
	}

	// The settlement is cleared, so its posted charge cannot be taken back
	// (BRY-555); the deposit is undone first.
	var cleared *ChargeInClearedSettlementError
	if _, _, err := svc.UnassignVippsCharge(ctx, club.ID, club.Treasurer.ID, toAccount); !errors.As(err, &cleared) {
		t.Fatalf("unassign in a cleared settlement: err = %v, want ChargeInClearedSettlementError", err)
	}
	if _, _, err := svc.UnassignBankRow(ctx, club.ID, club.Treasurer.ID, bankRow); err != nil {
		t.Fatalf("undo the deposit: %v", err)
	}

	// Unassigning a posted charge voids it with a reversal; the income nets
	// back to zero and the original stays for the audit trail.
	if _, _, err := svc.UnassignVippsCharge(ctx, club.ID, club.Treasurer.ID, toAccount); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	if got := entryStatus(t, svc, bilag.EntryID); got != "voided" {
		t.Errorf("unassigned entry status = %q, want voided", got)
	}
	if got := clubNet(t, svc, club, "3200"); got != 0 {
		t.Errorf("3200 net after unassign = %v, want 0", got)
	}
}

// A post that fails must not leave a draft behind or the charge marked as
// booked: the charge goes back to Tildel.
func TestVippsChargeFailedPostLeavesNoOrphan(t *testing.T) {
	svc, club := newAutopostClub(t)
	ctx := context.Background()

	// Make posting this one entry fail inside PostJournalEntry, after the
	// draft was created and the row marked.
	for _, stmt := range []string{
		`CREATE FUNCTION refuse_failpost() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN
		   IF NEW.status = 'posted' AND NEW.description LIKE '%FAILPOST%' THEN
		     RAISE EXCEPTION 'synthetic post failure';
		   END IF;
		   RETURN NEW;
		 END $$`,
		`CREATE TRIGGER refuse_failpost BEFORE UPDATE ON journal_entries
		 FOR EACH ROW EXECUTE FUNCTION refuse_failpost()`,
	} {
		if _, err := svc.db.Exec(ctx, stmt); err != nil {
			t.Fatalf("install failure trigger: %v", err)
		}
	}

	row := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 30000, CustomerName: "FAILPOST"})
	if _, err := svc.CreateVippsChargeBilag(ctx, club.ID, row, "3200", "", club.Treasurer.ID); err == nil {
		t.Fatal("expected the bilag to fail when posting fails")
	}

	var entries int
	var linked *string
	if err := svc.db.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM journal_entries WHERE club_id = $1), journal_entry_id::text
		   FROM vipps_import_rows WHERE id = $2`,
		club.ID, row,
	).Scan(&entries, &linked); err != nil {
		t.Fatalf("read state: %v", err)
	}
	if entries != 0 || linked != nil {
		t.Errorf("after a failed post: %d entries, row linked=%v; want 0 and unlinked", entries, linked)
	}
}

func TestClosePeriodRefusesWhileDraftsRemain(t *testing.T) {
	svc, club := newAutopostClub(t)
	ctx := context.Background()

	draft, err := svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: club.PeriodID,
		EntryDate:      "2026-03-01",
		Description:    "Synthetic draft",
		Source:         "manual",
		CreatedBy:      club.Treasurer.ID,
		ClubID:         club.ID,
		Lines: []CreateJournalLineInput{
			{AccountCode: "1920", Debit: 250},
			{AccountCode: "3900", Credit: 250},
		},
	})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}

	err = svc.ClosePeriod(ctx, club.ID, club.PeriodID, club.Treasurer.ID)
	var drafts *PeriodHasDraftsError
	if !errors.As(err, &drafts) {
		t.Fatalf("ClosePeriod err = %v, want PeriodHasDraftsError", err)
	}
	if len(drafts.Drafts) != 1 || drafts.Drafts[0].Source != "manual" ||
		drafts.Drafts[0].Count != 1 || drafts.Drafts[0].Total != 250 {
		t.Errorf("draft summary = %+v, want one manual draft of 250", drafts.Drafts)
	}

	if err := svc.DeleteDraftJournalEntry(ctx, club.ID, draft.ID); err != nil {
		t.Fatalf("delete draft: %v", err)
	}
	if err := svc.ClosePeriod(ctx, club.ID, club.PeriodID, club.Treasurer.ID); err != nil {
		t.Fatalf("close after clearing drafts: %v", err)
	}
}

// A draft booked from a Vipps or bank row can't be deleted directly: that
// would orphan the row's faktura link.
func TestDeleteDraftRefusesLinkedEntries(t *testing.T) {
	svc, club := newAutopostClub(t)
	ctx := context.Background()

	row := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 10000})
	draft, err := svc.CreateJournalEntry(ctx, CreateJournalEntryInput{
		FiscalPeriodID: club.PeriodID,
		EntryDate:      "2026-06-01",
		Description:    "Legacy-style Vipps draft",
		Source:         "vipps",
		CreatedBy:      club.Treasurer.ID,
		ClubID:         club.ID,
		Lines: []CreateJournalLineInput{
			{AccountCode: "2900", Debit: 100},
			{AccountCode: "3900", Credit: 100},
		},
	})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if _, err := svc.db.Exec(ctx,
		`UPDATE vipps_import_rows SET journal_entry_id = $1 WHERE id = $2`, draft.ID, row,
	); err != nil {
		t.Fatalf("link: %v", err)
	}
	if err := svc.DeleteDraftJournalEntry(ctx, club.ID, draft.ID); !errors.Is(err, ErrEntryLinked) {
		t.Errorf("delete linked draft err = %v, want ErrEntryLinked", err)
	}
}
