package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// A Vipps charge's suggestions are a board read like the rest of the
// accounting group: members are refused, and a charge of another club is
// not found rather than ranked.
func TestVippsChargeSuggestionsRoute(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := accounting.NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	other := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	h := NewAccountingHandler(svc, nil, nil, nil, nil, zerolog.Nop())

	r := chi.NewRouter()
	r.Use(middleware.RequireRoleOrBoardRead("treasurer", "board", "admin"))
	r.Get("/vipps-charges/{rowID}/suggestions", h.HandleVippsChargeSuggestions)
	get := func(userID, clubID string, roles []string, chargeID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/vipps-charges/"+chargeID+"/suggestions", nil)
		claims := &auth.Claims{UserID: userID, ClubID: clubID, Roles: roles}
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	own := club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Kari Testdotter"})
	foreign := other.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Ola Testmann"})

	rec := get(club.Treasurer.ID, club.ID, []string{"treasurer"}, own)
	var res accounting.BankRowSuggestions
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &res) != nil || len(res.Accounts) == 0 {
		t.Fatalf("own charge: %d %s", rec.Code, rec.Body)
	}

	member := club.NewMember(t, "member")
	if rec := get(member.ID, club.ID, []string{"member"}, own); rec.Code != http.StatusForbidden {
		t.Errorf("member: got %d, want 403", rec.Code)
	}

	rec = get(club.Treasurer.ID, club.ID, []string{"treasurer"}, foreign)
	if rec.Code != http.StatusNotFound {
		t.Errorf("other club's charge: got %d %s, want 404", rec.Code, rec.Body)
	}
	if rec := get(club.Treasurer.ID, club.ID, []string{"treasurer"}, "not-a-uuid"); rec.Code != http.StatusNotFound {
		t.Errorf("malformed charge id: got %d %s, want 404", rec.Code, rec.Body)
	}
}

// The candidate fakturas of a Vipps charge are found by its amount alone
// (a charge has no bank row to load), for every accounting role.
func TestVippsChargePotentialInvoicesRoute(t *testing.T) {
	env := newAliasTestEnv(t, func(r chi.Router, svc *accounting.Service, _ *audit.Service) {
		h := NewAccountingHandler(svc, nil, nil, nil, nil, zerolog.Nop())
		r.Get("/vipps-charges/{rowID}/potential-invoices", h.HandleVippsChargePotentialInvoices)
	})
	inv := env.club.NewInvoice(t, fin.InvoiceOpts{Cents: 45000})
	env.club.NewInvoice(t, fin.InvoiceOpts{Cents: 12300})
	charge := env.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 45000, CustomerName: "Kari Testdotter"})

	rec := env.serve([]string{"board"}, http.MethodGet, "/vipps-charges/"+charge+"/potential-invoices", nil)
	var res struct {
		Items     []accounting.InvoiceSuggestion `json:"items"`
		Potential bool                           `json:"potential"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &res) != nil ||
		len(res.Items) != 1 || res.Items[0].InvoiceID != inv.ID || !res.Potential {
		t.Fatalf("potential invoices: %d %s", rec.Code, rec.Body)
	}

	for _, id := range []string{uuid.NewString(), "not-a-uuid"} {
		rec = env.serve([]string{"board"}, http.MethodGet, "/vipps-charges/"+id+"/potential-invoices", nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("charge %q: got %d %s, want 404", id, rec.Code, rec.Body)
		}
	}

	// A failure that is not "no such charge" is a server error, not a 404.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/vipps-charges/"+charge+"/potential-invoices", nil).WithContext(
		middleware.WithClaims(ctx, &auth.Claims{UserID: env.club.Treasurer.ID, ClubID: env.club.ID, Roles: []string{"board"}}))
	rec = httptest.NewRecorder()
	env.r.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("cancelled request: got %d %s, want 500", rec.Code, rec.Body)
	}
}
