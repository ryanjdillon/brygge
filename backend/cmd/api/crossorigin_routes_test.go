package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/brygge-klubb/brygge/internal/auth"
)

// The inert router's FRONTEND_URL; httptest requests have Host example.com.
const (
	testFrontendOrigin = "https://club.example.test"
	testRequestOrigin  = "http://example.com"
)

// serveWith serves method path with the given headers and no body.
func serveWith(r http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func isCrossSiteRefusal(rec *httptest.ResponseRecorder) bool {
	if rec.Code != http.StatusForbidden {
		return false
	}
	var body map[string]any
	return json.Unmarshal(rec.Body.Bytes(), &body) == nil && body["error"] == codeCrossSiteRequest
}

func newMiniredisRouter(t *testing.T) http.Handler {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return inertRouterWithRedis(t, auth.NewSessionService(nil), nil, rdb)
}

// A browser POST another site makes is refused with 403 cross_site_request
// (BRY-660); one from the app's own origin, or from a client that sends no
// fetch metadata at all, reaches the route. POST /join/confirm is the probe:
// past the guard its limiter fails open and the handler answers 415 for the
// missing JSON body, before it needs a database.
func TestCrossOriginGuard(t *testing.T) {
	r := inertRouter(t, auth.NewSessionService(nil), nil)
	const probe = "/api/v1/join/confirm"

	refused := map[string]map[string]string{
		"Sec-Fetch-Site cross-site": {"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example.test"},
		// A sibling subdomain (webmail, matrix) is same-site, not same-origin.
		"Sec-Fetch-Site same-site":       {"Sec-Fetch-Site": "same-site", "Origin": "https://mail.example.test"},
		"Sec-Fetch-Site same-site alone": {"Sec-Fetch-Site": "same-site"},
		"Origin null":                    {"Origin": "null"},
		"mismatched Origin":              {"Origin": "https://evil.example.test"},
		"Origin on another port":         {"Origin": "http://example.com:8443"},
	}
	for name, h := range refused {
		t.Run("refused/"+name, func(t *testing.T) {
			rec := serveWith(r, http.MethodPost, probe, h)
			if !isCrossSiteRefusal(rec) {
				t.Fatalf("%d %s, want 403 cross_site_request", rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want JSON", ct)
			}
		})
	}

	allowed := map[string]map[string]string{
		"Sec-Fetch-Site same-origin": {"Sec-Fetch-Site": "same-origin", "Origin": testRequestOrigin},
		"Sec-Fetch-Site none":        {"Sec-Fetch-Site": "none"},
		"same-origin Origin":         {"Origin": testRequestOrigin},
		"Origin is FRONTEND_URL":     {"Origin": testFrontendOrigin},
		// An API on another host than the SPA still serves the SPA.
		"FRONTEND_URL marked cross-site": {"Sec-Fetch-Site": "cross-site", "Origin": testFrontendOrigin},
		"no headers":                     {},
	}
	for name, h := range allowed {
		t.Run("allowed/"+name, func(t *testing.T) {
			rec := serveWith(r, http.MethodPost, probe, h)
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("%d %s, want the handler's 415", rec.Code, rec.Body.String())
			}
		})
	}
}

// Every unsafe method is guarded; safe ones are not, so a cross-site link
// or image still loads.
func TestCrossOriginGuardMethods(t *testing.T) {
	r := inertRouter(t, auth.NewSessionService(nil), nil)
	cross := map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example.test"}

	for _, rt := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/auth/verify"},
		{http.MethodPost, "/api/v1/auth/verify/peek"},
		{http.MethodPost, "/api/v1/auth/magic-link"},
		{http.MethodPost, "/api/v1/contact"},
		{http.MethodPost, "/api/v1/join"},
		{http.MethodPut, "/api/v1/harbor/layout"},
		{http.MethodPatch, "/api/v1/auth/passkeys/11111111-1111-1111-1111-111111111111"},
		{http.MethodDelete, "/api/v1/auth/passkeys/11111111-1111-1111-1111-111111111111"},
		{http.MethodPost, "/api/v1/auth/session/logout"},
	} {
		if rec := serveWith(r, rt.method, rt.path, cross); !isCrossSiteRefusal(rec) {
			t.Errorf("cross-site %s %s: %d %s, want 403 cross_site_request", rt.method, rt.path, rec.Code, rec.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/join/confirm/some-token", "/api/v1/auth/verify?token=x", "/api/v1/health"} {
		if rec := serveWith(r, http.MethodGet, path, cross); isCrossSiteRefusal(rec) {
			t.Errorf("cross-site GET %s was refused; only unsafe methods are guarded", path)
		}
	}
}

// The routes other sites and servers call on purpose stay reachable
// cross-site: they authenticate by signature or token, not by cookie.
func TestCrossOriginGuardBypass(t *testing.T) {
	r := inertRouter(t, auth.NewSessionService(nil), nil)
	for _, h := range []map[string]string{
		{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example.test"},
		{"Origin": "https://mail.provider.example.test"},
		{"Origin": "null"},
	} {
		for _, path := range []string{
			"/api/v1/vipps/webhooks",
			"/api/v1/stripe/webhooks",
			"/api/v1/unsubscribe",
			"/api/v1/dev/query",
		} {
			if rec := serveWith(r, http.MethodPost, path, h); isCrossSiteRefusal(rec) {
				t.Errorf("POST %s with %v refused as cross-site; it must be bypassed", path, h)
			}
		}
	}
	// The bypass is that exact route, not its neighbours: the admin dev
	// query is cookie-authenticated and stays guarded.
	for _, h := range []map[string]string{
		{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example.test"},
		{"Sec-Fetch-Site": "same-site", "Origin": "https://mail.example.test"},
	} {
		if rec := serveWith(r, http.MethodPost, "/api/v1/admin/dev/query", h); !isCrossSiteRefusal(rec) {
			t.Errorf("POST /admin/dev/query with %v: %d %s, want 403 cross_site_request", h, rec.Code, rec.Body.String())
		}
	}
	if rec := serveWith(r, http.MethodPost, "/api/v1/join", map[string]string{"Origin": "null"}); !isCrossSiteRefusal(rec) {
		t.Errorf("POST /join with Origin null: %d, want 403", rec.Code)
	}
}

// The guard sits ahead of the rate limiters: refused cross-site requests do
// not spend the visitor's strict login budget, so an attacker cannot lock a
// member out of logging in from another site.
func TestCrossOriginGuardSpendsNoBudget(t *testing.T) {
	r := newMiniredisRouter(t)
	cross := map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example.test"}

	for i := range 6 {
		if rec := serveWith(r, http.MethodPost, "/api/v1/auth/verify", cross); !isCrossSiteRefusal(rec) {
			t.Fatalf("cross-site request %d: %d %s, want 403", i+1, rec.Code, rec.Body.String())
		}
	}
	for i := range 11 {
		if rec := serveWith(r, http.MethodPost, "/api/v1/auth/verify/peek", cross); !isCrossSiteRefusal(rec) {
			t.Fatalf("cross-site peek %d: %d %s, want 403", i+1, rec.Code, rec.Body.String())
		}
	}
	same := map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": testRequestOrigin}
	if rec := serveWith(r, http.MethodPost, "/api/v1/auth/verify", same); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("same-origin verify after cross-site ones: %d, want 415 (budget untouched)", rec.Code)
	}
	if rec := serveWith(r, http.MethodPost, "/api/v1/auth/verify/peek", same); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("same-origin peek after cross-site ones: %d, want 415 (budget untouched)", rec.Code)
	}
}

// CORS preflights are answered by the CORS handler ahead of the guard: the
// app's origin gets its allow headers, and another origin none, but neither
// is the guard's 403.
func TestCrossOriginGuardPreflight(t *testing.T) {
	r := inertRouter(t, auth.NewSessionService(nil), nil)
	preflight := func(origin, site string) *httptest.ResponseRecorder {
		return serveWith(r, http.MethodOptions, "/api/v1/auth/verify", map[string]string{
			"Origin":                         origin,
			"Sec-Fetch-Site":                 site,
			"Access-Control-Request-Method":  http.MethodPost,
			"Access-Control-Request-Headers": "content-type",
		})
	}

	rec := preflight(testFrontendOrigin, "cross-site")
	if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("preflight from FRONTEND_URL: %d %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != testFrontendOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, testFrontendOrigin)
	}
	if got := strings.ToLower(rec.Header().Get("Access-Control-Allow-Headers")); !strings.Contains(got, "content-type") {
		t.Errorf("Access-Control-Allow-Headers = %q, want content-type", got)
	}

	rec = preflight("https://evil.example.test", "cross-site")
	if isCrossSiteRefusal(rec) {
		t.Fatal("a preflight reached the guard")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("a foreign origin's preflight got Access-Control-Allow-Origin %q", got)
	}
}

// The peek has its own fail-closed per-IP budget of 10 a minute, separate
// from the strict login budget in both directions: opening links does not
// use up the button's logins, and failed logins do not stop the page from
// showing the account.
func TestLoginPeekRateLimit(t *testing.T) {
	t.Run("peeks spend only the peek budget", func(t *testing.T) {
		r := newMiniredisRouter(t)
		for i := range 10 {
			if rec := serveAs(r, http.MethodPost, "/api/v1/auth/verify/peek", ""); rec.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("peek %d: %d %s, want 415", i+1, rec.Code, rec.Body.String())
			}
		}
		if rec := serveAs(r, http.MethodPost, "/api/v1/auth/verify/peek", ""); rec.Code != http.StatusTooManyRequests {
			t.Fatalf("peek 11: %d, want 429", rec.Code)
		}
		if rec := serveAs(r, http.MethodPost, "/api/v1/auth/verify", ""); rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("verify after the peek budget: %d, want 415 (strict budget untouched)", rec.Code)
		}
	})
	t.Run("logins spend only the strict budget", func(t *testing.T) {
		r := newMiniredisRouter(t)
		for range 5 {
			serveAs(r, http.MethodPost, "/api/v1/auth/verify", "")
		}
		if rec := serveAs(r, http.MethodPost, "/api/v1/auth/verify", ""); rec.Code != http.StatusTooManyRequests {
			t.Fatalf("verify 6: %d, want 429", rec.Code)
		}
		if rec := serveAs(r, http.MethodPost, "/api/v1/auth/verify/peek", ""); rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("peek after the strict budget: %d, want 415 (peek budget untouched)", rec.Code)
		}
	})
}

func TestOriginOf(t *testing.T) {
	for in, want := range map[string]string{
		"https://club.example.test":         "https://club.example.test",
		"https://club.example.test/":        "https://club.example.test",
		"http://localhost:5173":             "http://localhost:5173",
		"https://club.example.test/app?x=1": "https://club.example.test",
		"https://club.example.test:443":     "https://club.example.test",
		"http://club.example.test:80/":      "http://club.example.test",
		"HTTPS://club.example.test:443":     "https://club.example.test",
		"https://club.example.test:8443":    "https://club.example.test:8443",
		"http://club.example.test:443":      "http://club.example.test:443",
		"https://[2001:db8::1]:443":         "https://[2001:db8::1]",
		"":                                  "",
		"club.example.test":                 "",
	} {
		if got := originOf(in); got != want {
			t.Errorf("originOf(%q) = %q, want %q", in, got, want)
		}
	}
}
