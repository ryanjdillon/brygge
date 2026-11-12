package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

func newPairTransferEnv(t *testing.T) (*aliasTestEnv, *accounting.Service) {
	t.Helper()
	var svc *accounting.Service
	env := newAliasTestEnv(t, func(r chi.Router, s *accounting.Service, as *audit.Service) {
		svc = s
		bh := NewBankRowsHandler(s, as, zerolog.Nop())
		h := NewAccountingHandler(s, as, nil, nil, nil, zerolog.Nop())
		r.Post("/bank-rows/pair-transfer", bh.HandlePairTransfer)
		r.Patch("/bank-import/{importID}/account", h.HandleReassignBankImport)
	})
	return env, svc
}

// pairRows seeds an outgoing row on 1920 and an incoming row on 1925.
func pairRows(t *testing.T, c *fin.Club, cents int64) (out, in, outImport string) {
	t.Helper()
	date := time.Date(c.Year, 6, 3, 0, 0, 0, 0, time.UTC)
	outImport = c.NewBankImportOn(t, "1920")
	out = c.NewBankRow(t, outImport, fin.BankRowOpts{Date: date, Cents: -cents, Description: "Synthetic transfer out"})
	in = c.NewBankRow(t, c.NewBankImportOn(t, "1925"), fin.BankRowOpts{Date: date, Cents: cents, Description: "Synthetic transfer in"})
	return out, in, outImport
}

// Par som overføring answers 200 with the result and one audit row per bank
// row; refusals map to 409 with a code, a foreign row to 404 and a bad
// count to 400.
func TestPairTransferHandler(t *testing.T) {
	env, _ := newPairTransferEnv(t)
	c := env.club
	ctx := context.Background()
	out, in, _ := pairRows(t, c, 120000)
	stickyDecline := func(id string) {
		if _, err := c.Pool.Exec(ctx, `UPDATE bank_import_rows SET auto_declined_at = now() WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	stickyDecline(out)

	rec := env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/pair-transfer",
		map[string]any{"row_ids": []string{in, out}, "description": "Synthetic savings"})
	if rec.Code != http.StatusOK {
		t.Fatalf("pair: %d %s", rec.Code, rec.Body)
	}
	var res accounting.TransferPairResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.JournalEntryID == "" || res.OutboundRowID != out || res.InboundRowID != in ||
		res.DebitAccount != "1925" || res.CreditAccount != "1920" || res.Amount != 1200 ||
		len(res.LiftedDecline) != 1 || res.LiftedDecline[0] != out {
		t.Errorf("result = %+v", res)
	}
	rows := env.audit(audit.ActionBankRowTransferPaired)
	if len(rows) != 2 {
		t.Fatalf("audit rows = %d, want 2", len(rows))
	}
	seen := map[string]bool{}
	for _, d := range rows {
		other, _ := d["other_row_id"].(string)
		seen[other] = true
		if d["journal_entry_id"] != res.JournalEntryID || d["debit_account"] != "1925" || d["credit_account"] != "1920" ||
			d["amount"] != float64(1200) || d["description"] != "Synthetic savings" || d["entry_date"] != res.EntryDate {
			t.Errorf("audit details = %v", d)
		}
		if lifted, _ := d["lifted_decline"].(bool); lifted != (other == in) {
			t.Errorf("audit lifted_decline = %v for the row whose other side is %s", d["lifted_decline"], other)
		}
	}
	if !seen[in] || !seen[out] {
		t.Errorf("audit other_row_id = %v, want each side to name the other", seen)
	}

	// Pairing the same rows again: 409 row_booked.
	rec = env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/pair-transfer", map[string]any{"row_ids": []string{out, in}})
	var body map[string]string
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["code"] != accounting.TransferRowBooked || body["error"] == "" {
		t.Errorf("pair again: %d %s, want 409 row_booked", rec.Code, rec.Body)
	}

	// Two rows on the same account: 409 transfer_same_account.
	imp := c.NewBankImportOn(t, "1920")
	a := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: -5000})
	b := c.NewBankRow(t, imp, fin.BankRowOpts{Cents: 5000})
	rec = env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/pair-transfer", map[string]any{"row_ids": []string{a, b}})
	body = nil
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["code"] != accounting.TransferSameAccount {
		t.Errorf("same account: %d %s, want 409 transfer_same_account", rec.Code, rec.Body)
	}

	// A Vipps payout deposit: 409 provider_payout.
	vipps := c.NewBankRow(t, c.NewBankImportOn(t, "1925"), fin.BankRowOpts{Cents: 7000, Description: "Utb. 2000591 Vippsnr 100001"})
	vOut := c.NewBankRow(t, c.NewBankImportOn(t, "1920"), fin.BankRowOpts{Cents: -7000})
	rec = env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/pair-transfer", map[string]any{"row_ids": []string{vOut, vipps}})
	body = nil
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["code"] != providerPayoutCode {
		t.Errorf("provider payout: %d %s, want 409 provider_payout", rec.Code, rec.Body)
	}

	// Another club's row: 404, revealing nothing.
	other := fin.NewClub(t, c.Pool)
	foreign := other.NewBankRow(t, other.NewBankImportOn(t, "1925"), fin.BankRowOpts{Cents: 5000})
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/pair-transfer",
		map[string]any{"row_ids": []string{a, foreign}}); rec.Code != http.StatusNotFound {
		t.Errorf("foreign row: %d %s, want 404", rec.Code, rec.Body)
	}

	// One row, the same row twice, three rows, a malformed id: 400.
	for name, ids := range map[string][]string{
		"one":       {a},
		"twice":     {a, a},
		"three":     {a, b, vOut},
		"malformed": {a, "not-a-uuid"},
	} {
		if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/bank-rows/pair-transfer",
			map[string]any{"row_ids": ids}); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, rec.Code, rec.Body)
		}
	}
	if n := len(env.audit(audit.ActionBankRowTransferPaired)); n != 2 {
		t.Errorf("a refusal was audited: %d rows", n)
	}
}

// Reassigning an import's bank account swaps only that import's line on a
// transfer's entry, and refuses a target the entry already uses (both lines
// would land on one account).
func TestReassignBankImportWithTransfer(t *testing.T) {
	env, svc := newPairTransferEnv(t)
	c := env.club
	ctx := context.Background()
	if _, err := c.Pool.Exec(ctx, `INSERT INTO accounts (club_id, code, name, account_type, is_system)
		VALUES ($1, '1930', 'Synthetic bank three', 'asset', false) ON CONFLICT (club_id, code) DO NOTHING`, c.ID); err != nil {
		t.Fatal(err)
	}
	out, in, outImport := pairRows(t, c, 80000)
	res, err := svc.PairBankTransfer(ctx, c.ID, c.Treasurer.ID, []string{out, in}, "")
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	lines := func() map[string][2]float64 {
		t.Helper()
		rows, err := c.Pool.Query(ctx,
			`SELECT a.code, jl.debit::float8, jl.credit::float8 FROM journal_lines jl
			   JOIN accounts a ON a.id = jl.account_id WHERE jl.journal_entry_id = $1`, res.JournalEntryID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		m := map[string][2]float64{}
		for rows.Next() {
			var code string
			var d, cr float64
			if err := rows.Scan(&code, &d, &cr); err != nil {
				t.Fatal(err)
			}
			m[code] = [2]float64{d, cr}
		}
		return m
	}

	rec := env.serve([]string{"treasurer"}, http.MethodPatch, "/bank-import/"+outImport+"/account", map[string]any{"bank_account_code": "1925"})
	var body map[string]any
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["code"] != reassignCollidesTransferCode {
		t.Errorf("reassign onto the other side's account: %d %s, want 409 %s", rec.Code, rec.Body, reassignCollidesTransferCode)
	}
	if got := lines(); got["1925"] != [2]float64{800, 0} || got["1920"] != [2]float64{0, 800} {
		t.Errorf("refused reassign changed the lines: %v", got)
	}

	rec = env.serve([]string{"treasurer"}, http.MethodPatch, "/bank-import/"+outImport+"/account", map[string]any{"bank_account_code": "1930"})
	if rec.Code != http.StatusOK {
		t.Fatalf("reassign to a third account: %d %s", rec.Code, rec.Body)
	}
	if got := lines(); len(got) != 2 || got["1925"] != [2]float64{800, 0} || got["1930"] != [2]float64{0, 800} {
		t.Errorf("lines after the reassign = %v; want DR 1925 / CR 1930", got)
	}
}

// The reassign's refusals keep their HTTP shape now that the work is
// accounting.ReassignBankImport (BRY-674): an id that is not a UUID or not
// the club's import is 404, an unknown target account 400, and the same
// account 200 "unchanged" with nothing written.
func TestReassignBankImportRefusals(t *testing.T) {
	env, _ := newPairTransferEnv(t)
	c := env.club
	imp := c.NewBankImportOn(t, "1920")
	patch := func(importID, code string) (int, map[string]any) {
		t.Helper()
		rec := env.serve([]string{"treasurer"}, http.MethodPatch, "/bank-import/"+importID+"/account", map[string]any{"bank_account_code": code})
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}
	if code, body := patch("not-a-uuid", "1925"); code != http.StatusNotFound {
		t.Errorf("malformed import id: %d %v, want 404", code, body)
	}
	if code, body := patch("00000000-0000-4000-8000-000000000000", "1925"); code != http.StatusNotFound {
		t.Errorf("unknown import id: %d %v, want 404", code, body)
	}
	if code, body := patch(imp, "1999"); code != http.StatusBadRequest {
		t.Errorf("unknown target account: %d %v, want 400", code, body)
	}
	if code, body := patch(imp, "1920"); code != http.StatusOK || body["status"] != "unchanged" {
		t.Errorf("same account: %d %v, want 200 unchanged", code, body)
	}
	var got string
	if err := c.Pool.QueryRow(context.Background(), `SELECT bank_account_code FROM bank_imports WHERE id = $1`, imp).Scan(&got); err != nil || got != "1920" {
		t.Errorf("import account %q (err %v) after refusals, want 1920", got, err)
	}
}
