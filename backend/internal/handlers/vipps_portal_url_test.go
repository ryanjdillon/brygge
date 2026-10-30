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
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

const syntheticPortalURL = "https://portal.vippsmobilepay.com/100200/home"

func TestNormalizeVippsPortalURL(t *testing.T) {
	longPath := "https://portal.vippsmobilepay.com/" + strings.Repeat("a", maxVippsPortalURLLen)
	exact := "https://portal.vippsmobilepay.com/" + strings.Repeat("a", maxVippsPortalURLLen-len("https://portal.vippsmobilepay.com/"))
	tests := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{"valid", syntheticPortalURL, syntheticPortalURL, true},
		{"valid without a path", "https://portal.vippsmobilepay.com", "https://portal.vippsmobilepay.com", true},
		{"valid with a query", syntheticPortalURL + "?tab=sales", syntheticPortalURL + "?tab=sales", true},
		{"padding is trimmed", "  " + syntheticPortalURL + "\n", syntheticPortalURL, true},
		{"empty clears", "", "", true},
		{"blank clears", "   ", "", true},
		{"exactly at the limit", exact, exact, true},
		{"too long", longPath, "", false},
		{"wrong host", "https://portal.example.com/100200/home", "", false},
		{"subdomain of the host", "https://evil.portal.vippsmobilepay.com/100200/home", "", false},
		{"host as a suffix", "https://portal.vippsmobilepay.com.evil.test/100200/home", "", false},
		{"host with a trailing dot", "https://portal.vippsmobilepay.com./100200/home", "", false},
		{"http", "http://portal.vippsmobilepay.com/100200/home", "", false},
		{"other scheme", "javascript://portal.vippsmobilepay.com/%0aalert(1)", "", false},
		{"userinfo", "https://user@portal.vippsmobilepay.com/100200/home", "", false},
		{"userinfo disguising the host", "https://portal.vippsmobilepay.com@evil.test/home", "", false},
		{"backslash disguising the host", `https://evil.test\@portal.vippsmobilepay.com/home`, "", false},
		{"port", "https://portal.vippsmobilepay.com:8443/100200/home", "", false},
		{"default port", "https://portal.vippsmobilepay.com:443/100200/home", "", false},
		{"fragment", syntheticPortalURL + "#top", "", false},
		{"empty fragment", syntheticPortalURL + "#", "", false},
		{"relative", "/100200/home", "", false},
		{"host without a scheme", "portal.vippsmobilepay.com/100200/home", "", false},
		{"protocol relative", "//portal.vippsmobilepay.com/100200/home", "", false},
		{"inner whitespace", "https://portal.vippsmobilepay.com/100200 /home", "", false},
		{"control character", "https://portal.vippsmobilepay.com/100200/\x00home", "", false},
		{"right-to-left override", "https://portal.vippsmobilepay.com/100200/\u202ehome", "", false},
		{"zero-width space", "https://portal.vippsmobilepay.com/100\u200b200/home", "", false},
		{"zero-width space in the host", "https://portal.vippsmobilepay.com\u200b/100200/home", "", false},
		{"byte order mark", "\ufeffhttps://portal.vippsmobilepay.com/100200/home", "", false},
		{"opaque", "https:portal.vippsmobilepay.com", "", false},
		{"not a url", "https://%zz", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := normalizeVippsPortalURL(tc.in)
			if ok != tc.wantOK || got != tc.want {
				t.Errorf("normalizeVippsPortalURL(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestProviderLinksVippsPortal(t *testing.T) {
	live := config.Config{VippsMSN: "698382"}
	tests := []struct {
		name   string
		cfg    config.Config
		stored string
		want   string
	}{
		{"stored address is the link", live, syntheticPortalURL, syntheticPortalURL},
		{"no stored address", live, "", ""},
		{"test mode hides it", config.Config{VippsMSN: "698382", VippsTestMode: true}, syntheticPortalURL, ""},
		{"mock hides it", config.Config{VippsMSN: "mock-msn", VippsTestMode: true}, syntheticPortalURL, ""},
		{"Vipps not configured", config.Config{}, syntheticPortalURL, ""},
		{"an address written around the endpoint is not linked", live, "https://evil.test/100200/home", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := providerLinksFor(&tc.cfg, tc.stored).Vipps; got != tc.want {
				t.Errorf("Vipps link = %q, want %q", got, tc.want)
			}
		})
	}
}

// The setting round-trips through the settings endpoint: an address outside
// the portal is refused with the typed code and changes nothing, an omitted
// field keeps it, and an empty one clears it.
func TestFinancialSettingsVippsPortalURL(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	club := fin.NewClub(t, pool)
	h := &ClubSettingsHandler{db: pool, config: &config.Config{}, log: zerolog.Nop()}
	claims := &auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"treasurer"}}

	update := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/x", strings.NewReader(body))
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
		rec := httptest.NewRecorder()
		h.HandleUpdateFinancialSettings(rec, req)
		return rec
	}
	stored := func() string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
		rec := httptest.NewRecorder()
		h.HandleGetFinancialSettings(rec, req)
		var s clubFinancialSettings
		if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
			t.Fatalf("get settings: %v (%s)", err, rec.Body)
		}
		return s.VippsPortalURL
	}
	column := func() *string {
		t.Helper()
		var v *string
		if err := pool.QueryRow(context.Background(),
			`SELECT vipps_portal_url FROM clubs WHERE id = $1`, club.ID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	if got := stored(); got != "" {
		t.Errorf("default = %q, want none", got)
	}
	if column() != nil {
		t.Error("default column is not NULL")
	}

	rec := update(`{"vipps_portal_url":"https://portal.example.com/1/home"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("wrong host: status %d, want 400", rec.Code)
	}
	var refusal struct{ Code string }
	if err := json.Unmarshal(rec.Body.Bytes(), &refusal); err != nil || refusal.Code != codeInvalidVippsPortalURL {
		t.Errorf("refusal = %s, want code %q", rec.Body, codeInvalidVippsPortalURL)
	}
	if got := stored(); got != "" {
		t.Errorf("a refused update stored %q", got)
	}

	if rec := update(`{"vipps_portal_url":"  ` + syntheticPortalURL + ` "}`); rec.Code != http.StatusOK {
		t.Fatalf("valid update: %d %s", rec.Code, rec.Body)
	}
	if got := stored(); got != syntheticPortalURL {
		t.Errorf("after update = %q, want the trimmed address", got)
	}

	if rec := update(`{"payment_fee_account":"7700"}`); rec.Code != http.StatusOK {
		t.Fatalf("unrelated update: %d %s", rec.Code, rec.Body)
	}
	if got := stored(); got != syntheticPortalURL {
		t.Errorf("an omitted field changed the address to %q", got)
	}
	if rec := update(`{"vipps_portal_url":"http://portal.vippsmobilepay.com/1/home"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("http: status %d, want 400", rec.Code)
	}
	if got := stored(); got != syntheticPortalURL {
		t.Errorf("a refused update changed the address to %q", got)
	}

	if rec := update(`{"vipps_portal_url":"  "}`); rec.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body)
	}
	if got := stored(); got != "" {
		t.Errorf("after clearing = %q, want none", got)
	}
	if column() != nil {
		t.Error("cleared column is not NULL")
	}
}
