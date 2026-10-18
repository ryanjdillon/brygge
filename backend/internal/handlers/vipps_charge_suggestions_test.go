package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
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
}
