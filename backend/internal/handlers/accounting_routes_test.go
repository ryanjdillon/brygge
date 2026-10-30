package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// ungatedLedgerWrites are the non-GET ledger routes deliberately left outside
// moneyGate: they only read, preview, or create drafts and attachments that
// a gated post has to pick up later.
var ungatedLedgerWrites = map[string]bool{
	"POST /journal/parse-receipt":                     true,
	"POST /journal/{entryID}/attachment":              true,
	"POST /sync/invoices/resplit/preview":             true,
	"POST /sync/vipps-drafts/preview":                 true,
	"POST /sync/vipps-fallback-reclass/preview":       true,
	"POST /bank-import/{importID}/rows/{rowID}/match": true,
	"POST /bank-import/{importID}/auto-match":         true,
}

// A board member can read the books and run previews but every route that
// can post or change a GL entry answers 403. Walking the mounted router keeps
// a new mutating route from slipping in without the gate.
func TestLedgerRoutesGateMoneyMovesForBoard(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := accounting.NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	h := NewAccountingHandler(svc, nil, nil, nil, nil, zerolog.Nop())

	r := chi.NewRouter()
	r.Use(middleware.RequireRole("treasurer", "board", "admin"))
	h.MountLedgerRoutes(r,
		middleware.RequireRole("treasurer", "admin"),
		middleware.RequireFreshTOTPDefault(),
	)

	board := &auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"board"}}
	serve := func(method, path string, body any) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		req := httptest.NewRequest(method, path, &buf)
		req = req.WithContext(middleware.WithClaims(req.Context(), board))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	gated := 0
	walked := map[string]bool{}
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/")
		if route == "" {
			route = "/"
		}
		if method == http.MethodGet || ungatedLedgerWrites[method+" "+route] {
			return nil
		}
		walked[method+" "+route] = true
		path := route
		for _, p := range []string{"{entryID}", "{importID}", "{rowID}", "{payoutID}", "{txnID}"} {
			path = strings.ReplaceAll(path, p, "00000000-0000-0000-0000-000000000001")
		}
		if rec := serve(method, path, map[string]string{}); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as board: status %d, want 403", method, route, rec.Code)
		}
		gated++
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if !walked["POST /sync/vipps-fallback-reclass"] {
		t.Errorf("POST /sync/vipps-fallback-reclass not walked: not mounted under the gate")
	}
	for _, a := range []string{"assign-invoice", "assign-account", "dismiss", "undismiss", "unassign", "rebook"} {
		if r := "POST /stripe/transactions/{txnID}/" + a; !walked[r] {
			t.Errorf("%s not walked: not mounted under the gate", r)
		}
	}
	if gated < 15 {
		t.Errorf("walked %d gated routes; the router lost routes", gated)
	}

	for _, p := range []string{"/sync/invoices/resplit/preview", "/sync/vipps-drafts/preview", "/sync/vipps-fallback-reclass/preview"} {
		if rec := serve(http.MethodPost, p, map[string]string{"period_id": club.PeriodID}); rec.Code != http.StatusOK {
			t.Errorf("POST %s as board: status %d (%s), want 200", p, rec.Code, rec.Body.String())
		}
	}
	if rec := serve(http.MethodGet, "/bank-imports", nil); rec.Code != http.StatusOK {
		t.Errorf("GET /bank-imports as board: status %d, want 200", rec.Code)
	}
}
