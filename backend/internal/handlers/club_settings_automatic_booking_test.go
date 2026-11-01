package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// clubs.automatic_booking (ACC-13) is off for a new club, is read by the
// settings GET, is written only when the PATCH names it, and an unrelated PATCH
// leaves it alone.
func TestIntegration_AutomaticBookingSetting(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	club := fin.NewClub(t, pool)
	h := &ClubSettingsHandler{db: pool, config: &config.Config{}, log: zerolog.Nop()}

	call := func(method, body string, handler http.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/x", strings.NewReader(body))
		req = req.WithContext(middleware.WithClaims(req.Context(),
			&auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"treasurer"}}))
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}
	enabled := func() bool {
		t.Helper()
		rec := call(http.MethodGet, "", h.HandleGetFinancialSettings)
		if rec.Code != http.StatusOK {
			t.Fatalf("get: %d %s", rec.Code, rec.Body)
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		v, ok := got["automatic_booking"].(bool)
		if !ok {
			t.Fatalf("automatic_booking missing from the settings response: %s", rec.Body)
		}
		return v
	}
	patch := func(body string) {
		t.Helper()
		if rec := call(http.MethodPatch, body, h.HandleUpdateFinancialSettings); rec.Code != http.StatusOK {
			t.Fatalf("patch %s: %d %s", body, rec.Code, rec.Body)
		}
	}

	if enabled() {
		t.Fatal("a new club has automatic booking on")
	}
	patch(`{"automatic_booking":true}`)
	if !enabled() {
		t.Error("automatic_booking:true was not stored")
	}
	patch(`{"phone":"+4700000002"}`)
	if !enabled() {
		t.Error("an unrelated PATCH switched automatic booking off")
	}
	patch(`{"automatic_booking":false}`)
	if enabled() {
		t.Error("automatic_booking:false was not stored")
	}
}
