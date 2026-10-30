package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// A charge whose settlement is cleared against the bank deposit cannot be
// taken back from the charge's Angre or from the ledger's void: both answer
// 409 charge_in_cleared_settlement naming the settlement (BRY-555), and
// nothing changes. Once the deposit is undone the same Angre succeeds.
func TestUnassignChargeInClearedSettlementRoutes(t *testing.T) {
	env := newAliasTestEnv(t, func(r chi.Router, svc *accounting.Service, as *audit.Service) {
		ah := NewAccountingHandler(svc, as, nil, nil, nil, zerolog.Nop())
		r.Post("/vipps-charges/{rowID}/unassign", ah.HandleUnassignVippsCharge)
		r.Post("/journal/{entryID}/void", ah.HandleVoidJournalEntry)
		bh := NewBankRowsHandler(svc, as, zerolog.Nop())
		r.Post("/bank-rows/{rowID}/unassign", bh.HandleUnassign)
	})
	club := env.club
	ctx := context.Background()
	svc := accounting.NewService(club.Pool, audit.NewService(club.Pool, zerolog.Nop()), zerolog.Nop())
	const msn, settlement = "000001", "3300101"
	booking := time.Date(club.Year, 6, 2, 0, 0, 0, 0, time.UTC)

	charge := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 125000, Date: booking, MSN: msn})
	club.NewVippsRow(t, fin.VippsRowOpts{RowType: "fee", Cents: -5000, Date: booking, MSN: msn})
	club.NewVippsRow(t, fin.VippsRowOpts{RowType: "payout", Cents: -120000, Date: booking, MSN: msn, Settlement: settlement})
	bankRow := club.NewBankRow(t, club.NewBankImport(t), fin.BankRowOpts{
		Date: booking.AddDate(0, 0, 1), Cents: 120000, Description: "Utb. " + settlement + " Vippsnr " + msn,
	})
	booked, err := svc.CreateVippsChargeBilag(ctx, club.ID, charge, "3200", "", club.Treasurer.ID)
	if err != nil {
		t.Fatalf("book charge: %v", err)
	}
	if res, err := svc.ClearVippsPayout(ctx, club.ID, bankRow, club.Treasurer.ID); err != nil || !res.Ready {
		t.Fatalf("clear: %+v, %v", res, err)
	}

	rec := env.serve([]string{"treasurer"}, http.MethodPost, "/vipps-charges/"+charge+"/unassign", map[string]any{})
	wantRefusalCode(t, rec, chargeInClearedSettlementCode)
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body["settlement_number"] != settlement || body["msn"] != msn || body["bank_row_id"] != bankRow {
		t.Errorf("body = %v, want settlement %s / %s and the deposit %s", body, settlement, msn, bankRow)
	}
	if !strings.Contains(body["error"], "Utb. "+settlement+" Vippsnr "+msn) {
		t.Errorf("message %q does not name the deposit to undo", body["error"])
	}
	wantRefusalCode(t, env.serve([]string{"treasurer"}, http.MethodPost, "/journal/"+booked.EntryID+"/void", nil),
		chargeInClearedSettlementCode)

	// The settlement's fee entry is refused from the ledger as well.
	var feeEntry string
	if err := club.Pool.QueryRow(ctx,
		`SELECT journal_entry_id::text FROM vipps_import_rows WHERE club_id = $1 AND row_type = 'fee'`, club.ID).Scan(&feeEntry); err != nil {
		t.Fatalf("read fee entry: %v", err)
	}
	wantRefusalCode(t, env.serve([]string{"treasurer"}, http.MethodPost, "/journal/"+feeEntry+"/void", nil), "vipps_settlement_entry")

	var status string
	var linked *string
	if err := club.Pool.QueryRow(ctx,
		`SELECT je.status, r.journal_entry_id::text FROM vipps_import_rows r
		   JOIN journal_entries je ON je.id = $2 WHERE r.id = $1`, charge, booked.EntryID,
	).Scan(&status, &linked); err != nil {
		t.Fatalf("read charge: %v", err)
	}
	if status != "posted" || linked == nil || *linked != booked.EntryID {
		t.Errorf("refused routes changed the charge: entry %s, linked %v", status, linked)
	}
	if got := env.audit(audit.ActionVippsChargeUnassigned); len(got) != 0 {
		t.Errorf("%d unassign audit rows for a refused unassign, want 0", len(got))
	}

	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/"+bankRow+"/unassign", map[string]any{"confirm": true}); rec.Code != http.StatusOK {
		t.Fatalf("undo the deposit: %d %s", rec.Code, rec.Body.String())
	}
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/vipps-charges/"+charge+"/unassign", map[string]any{}); rec.Code != http.StatusOK {
		t.Fatalf("unassign after undoing the deposit: %d %s", rec.Code, rec.Body.String())
	}
}
