package handlers

import (
	"context"
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
)

func TestNormalizePublicDescription(t *testing.T) {
	atLimit := strings.Repeat("å", maxPublicDescriptionLen) // multi-byte: the cap counts characters
	tests := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{"plain", "A small harbour club by the fjord", "A small harbour club by the fjord", true},
		{"padding is trimmed", "  Boats and coffee \t", "Boats and coffee", true},
		{"blank clears", "   ", "", true},
		{"empty clears", "", "", true},
		{"exactly at the limit", atLimit, atLimit, true},
		{"limit counted after trimming", "  " + atLimit + "  ", atLimit, true},
		{"one over the limit", atLimit + "a", "", false},
		{"inner line break", "First line\nsecond line", "", false},
		{"control character", "Boats\x00and coffee", "", false},
		{"markup is kept as text", "<b>Boats</b> & coffee", "<b>Boats</b> & coffee", true},
		{"left-to-right embedding", "Boats\u202acoffee", "", false},
		{"right-to-left embedding", "Boats\u202bcoffee", "", false},
		{"pop directional formatting", "Boats\u202ccoffee", "", false},
		{"left-to-right override", "Boats\u202dcoffee", "", false},
		{"right-to-left override", "Boats\u202ecoffee", "", false},
		{"left-to-right isolate", "Boats\u2066coffee", "", false},
		{"right-to-left isolate", "Boats\u2067coffee", "", false},
		{"first strong isolate", "Boats\u2068coffee", "", false},
		{"pop directional isolate", "Boats\u2069coffee", "", false},
		{"line separator", "Boats\u2028coffee", "", false},
		{"paragraph separator", "Boats\u2029coffee", "", false},
		{"zero-width space", "Boats\u200bcoffee", "", false},
		{"byte order mark", "\ufeffBoats and coffee", "", false},
		{"ZWJ emoji sequence passes", "Sailors welcome \U0001F9D1\u200d\U0001F692", "Sailors welcome \U0001F9D1\u200d\U0001F692", true},
		{"ZWNJ passes", "Boats\u200ccoffee", "Boats\u200ccoffee", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in
			ok := normalizePublicDescription(&got)
			if ok != tc.wantOK {
				t.Fatalf("normalizePublicDescription(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("normalizePublicDescription(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
	if !normalizePublicDescription(nil) {
		t.Error("an omitted description must pass")
	}
}

// A refused description is answered before the database is touched, with the
// typed code the settings page words.
func TestUpdateSettingsRefusesLongPublicDescription(t *testing.T) {
	h := &ClubSettingsHandler{config: &config.Config{}, log: zerolog.Nop()}
	body, _ := json.Marshal(map[string]string{"public_description": strings.Repeat("x", maxPublicDescriptionLen+1)})
	req := httptest.NewRequest(http.MethodPatch, "/x", strings.NewReader(string(body)))
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: "u", ClubID: "c", Roles: []string{"admin"}}))
	rec := httptest.NewRecorder()
	h.HandleUpdateFinancialSettings(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 (%s)", rec.Code, rec.Body)
	}
	var refusal struct{ Code string }
	if err := json.Unmarshal(rec.Body.Bytes(), &refusal); err != nil || refusal.Code != codeInvalidPublicDescription {
		t.Errorf("code = %q (%v), want %q", refusal.Code, err, codeInvalidPublicDescription)
	}
}

// The description round-trips through the settings endpoint, trimmed; an
// omitted field keeps it, a blank one clears it, and the public club info
// carries it for the landing page. A new club has none.
func TestPublicDescriptionSetting(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	var slug string
	if err := pool.QueryRow(ctx, `SELECT slug FROM clubs WHERE id = $1`, clubID).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	h := &ClubSettingsHandler{db: pool, config: &config.Config{ClubSlug: slug}, log: zerolog.Nop()}
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"admin"})
	claims := &auth.Claims{UserID: userID, ClubID: clubID, Roles: []string{"admin"}}

	update := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/x", strings.NewReader(body))
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
		rec := httptest.NewRecorder()
		h.HandleUpdateFinancialSettings(rec, req)
		return rec
	}
	settings := func() string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
		rec := httptest.NewRecorder()
		h.HandleGetFinancialSettings(rec, req)
		var s clubFinancialSettings
		if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
			t.Fatalf("get settings: %v (%s)", err, rec.Body)
		}
		return s.PublicDescription
	}
	public := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		h.HandleGetPublicClub(rec, httptest.NewRequest(http.MethodGet, "/api/v1/club", nil))
		var info map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
			t.Fatalf("get public club: %v (%s)", err, rec.Body)
		}
		v, ok := info["public_description"].(string)
		if !ok {
			t.Fatalf("public club info has no public_description string: %s", rec.Body)
		}
		return v
	}

	if got := settings(); got != "" {
		t.Errorf("default = %q, want none", got)
	}
	if got := public(); got != "" {
		t.Errorf("public default = %q, want none", got)
	}

	if rec := update(`{"public_description":"  Boats, coffee and a long pier  "}`); rec.Code != http.StatusOK {
		t.Fatalf("set: status %d (%s)", rec.Code, rec.Body)
	}
	const want = "Boats, coffee and a long pier"
	if got := settings(); got != want {
		t.Errorf("stored = %q, want %q (trimmed)", got, want)
	}
	if got := public(); got != want {
		t.Errorf("public = %q, want %q", got, want)
	}

	if rec := update(`{"public_description":"` + strings.Repeat("x", maxPublicDescriptionLen+1) + `"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("too long: status %d, want 400", rec.Code)
	}
	if got := settings(); got != want {
		t.Errorf("after a refusal = %q, want it unchanged", got)
	}

	if rec := update(`{"phone":"+47 00 00 00 00"}`); rec.Code != http.StatusOK {
		t.Fatalf("other field: status %d (%s)", rec.Code, rec.Body)
	}
	if got := settings(); got != want {
		t.Errorf("omitted field changed it to %q", got)
	}

	if rec := update(`{"public_description":"   "}`); rec.Code != http.StatusOK {
		t.Fatalf("clear: status %d (%s)", rec.Code, rec.Body)
	}
	if got := public(); got != "" {
		t.Errorf("after clearing = %q, want none", got)
	}

	var fields []string
	if err := pool.QueryRow(ctx,
		`SELECT ARRAY(SELECT jsonb_array_elements_text(details->'fields'))
		   FROM audit_log WHERE club_id = $1 AND action = 'update_financial_settings'
		  ORDER BY created_at DESC LIMIT 1`, clubID,
	).Scan(&fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0] != "public_description" {
		t.Errorf("audit fields = %v, want [public_description]", fields)
	}
}
