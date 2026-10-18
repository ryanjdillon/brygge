package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// A row with a strong partner is refused on its own (409 has_partner) until
// the operator confirms it; the pair books through assign-account-group.
func TestAssignAccountGuardsRowsWithPartner(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := accounting.NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	h := NewBankRowsHandler(svc, nil, zerolog.Nop())

	r := chi.NewRouter()
	r.Get("/bank-rows/{rowID}/partners", h.HandlePartners)
	r.Post("/bank-rows/{rowID}/assign-account", h.HandleAssignAccount)
	r.Post("/bank-rows/assign-account-group", h.HandleAssignAccountGroup)
	claims := &auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"treasurer"}}
	serve := func(method, path string, body any) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		req := httptest.NewRequest(method, path, &buf)
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	imp := club.NewBankImport(t)
	day := time.Date(club.Year, 8, 31, 0, 0, 0, 0, time.UTC)
	out := club.NewBankRow(t, imp, fin.BankRowOpts{Cents: -6300000, Date: day, ToAccount: "99990011111"})
	in := club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 100000, Date: day.AddDate(0, 0, 2), FromAccount: "99990011111"})
	lone := club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 45000, Date: day, Counterpart: "Synthetic Guest"})

	rec := serve(http.MethodGet, "/bank-rows/"+in+"/partners", nil)
	var partners struct {
		Items []accounting.PartnerSuggestion `json:"items"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &partners) != nil || len(partners.Items) != 1 || partners.Items[0].ID != out {
		t.Fatalf("partners: %d %s", rec.Code, rec.Body)
	}

	single := map[string]any{"account_code": "3900", "kind": "revenue"}
	rec = serve(http.MethodPost, "/bank-rows/"+in+"/assign-account", single)
	if rec.Code != http.StatusConflict {
		t.Fatalf("assign alone without confirm: status %d, want 409 (%s)", rec.Code, rec.Body)
	}
	var conflict struct {
		Error    string                         `json:"error"`
		Partners []accounting.PartnerSuggestion `json:"partners"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &conflict); err != nil || conflict.Error != "has_partner" || len(conflict.Partners) != 1 {
		t.Errorf("409 body = %s", rec.Body)
	}

	if rec := serve(http.MethodPost, "/bank-rows/"+lone+"/assign-account", single); rec.Code != http.StatusOK {
		t.Errorf("row without partner: status %d, want 200 (%s)", rec.Code, rec.Body)
	}

	rec = serve(http.MethodPost, "/bank-rows/assign-account-group",
		map[string]any{"row_ids": []string{out, in}, "account_code": "2050"})
	if rec.Code != http.StatusOK {
		t.Fatalf("group: status %d (%s)", rec.Code, rec.Body)
	}

	// With the partner booked, the guard has nothing to say; confirm_alone
	// books a row that still has one.
	out2 := club.NewBankRow(t, imp, fin.BankRowOpts{Cents: -6300000, Date: day, ToAccount: "99990022222"})
	in2 := club.NewBankRow(t, imp, fin.BankRowOpts{Cents: 100000, Date: day, FromAccount: "99990022222"})
	single["confirm_alone"] = true
	if rec := serve(http.MethodPost, "/bank-rows/"+in2+"/assign-account", single); rec.Code != http.StatusOK {
		t.Errorf("confirm_alone: status %d, want 200 (%s)", rec.Code, rec.Body)
	}
	delete(single, "confirm_alone")
	single["kind"] = "expense"
	single["account_code"] = "6300"
	if rec := serve(http.MethodPost, "/bank-rows/"+out2+"/assign-account", single); rec.Code != http.StatusOK {
		t.Errorf("partner already booked: status %d, want 200 (%s)", rec.Code, rec.Body)
	}

	var linked int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM bank_import_rows WHERE id = ANY($1::uuid[]) AND journal_entry_id IS NOT NULL`,
		[]string{out, in, lone, out2, in2},
	).Scan(&linked); err != nil || linked != 5 {
		t.Errorf("%d of 5 rows booked (%v)", linked, err)
	}

	if rec := serve(http.MethodGet, "/bank-rows/00000000-0000-0000-0000-000000000001/partners", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown row: status %d, want 404", rec.Code)
	}
}
