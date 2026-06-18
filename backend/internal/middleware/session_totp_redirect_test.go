package middleware

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// DIL-334: a top-level browser navigation to a TOTP-gated GET (e.g. opening
// an inline faktura PDF after the step-up window lapsed) must 302 to the
// verify page instead of rendering the raw JSON 403. XHR callers keep the
// JSON shape the SPA interceptor relies on. These lock that behaviour in.

func TestIsBrowserNavigation(t *testing.T) {
	cases := []struct {
		name     string
		method   string
		secFetch string
		accept   string
		wantNav  bool
	}{
		{"sec-fetch navigate", http.MethodGet, "navigate", "", true},
		{"accept text/html", http.MethodGet, "", "text/html,application/xhtml+xml", true},
		{"accept html+json prefers xhr", http.MethodGet, "", "text/html,application/json", false},
		{"accept application/json", http.MethodGet, "", "application/json", false},
		{"post is never navigation", http.MethodPost, "navigate", "text/html", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "/api/v1/admin/financials/invoices/x/pdf", nil)
			if c.secFetch != "" {
				req.Header.Set("Sec-Fetch-Mode", c.secFetch)
			}
			if c.accept != "" {
				req.Header.Set("Accept", c.accept)
			}
			if got := isBrowserNavigation(req); got != c.wantNav {
				t.Errorf("isBrowserNavigation = %v, want %v", got, c.wantNav)
			}
		})
	}
}

func TestWriteTOTPRequiredRedirectsBrowserNavigation(t *testing.T) {
	const path = "/api/v1/admin/financials/invoices/abc/pdf"

	// Browser navigation → 302 to the verify page, original URL in ?next=.
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	rec := httptest.NewRecorder()
	writeTOTPRequired(rec, req, "totp_required")

	if rec.Code != http.StatusFound {
		t.Fatalf("navigation: status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/admin/verify-totp?next=") {
		t.Fatalf("Location = %q, want /admin/verify-totp?next=…", loc)
	}
	if next := nextParam(t, loc); next != path {
		t.Errorf("next = %q, want %q", next, path)
	}

	// XHR → 403 JSON (SPA-interceptor contract unchanged).
	xhr := httptest.NewRequest(http.MethodGet, path, nil)
	xhr.Header.Set("Accept", "application/json")
	xrec := httptest.NewRecorder()
	writeTOTPRequired(xrec, xhr, "totp_required")
	if xrec.Code != http.StatusForbidden {
		t.Fatalf("xhr: status = %d, want 403", xrec.Code)
	}
	if ct := xrec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("xhr: Content-Type = %q, want application/json", ct)
	}
	if !strings.Contains(xrec.Body.String(), `"totp_required"`) {
		t.Errorf("xhr: body = %q, want totp_required JSON", xrec.Body.String())
	}
}

func TestWriteTOTPFreshRequiredRedirectsBrowserNavigation(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounting/x", nil)
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	rec := httptest.NewRecorder()
	writeTOTPFreshRequired(rec, req, 5*time.Minute)
	if rec.Code != http.StatusFound {
		t.Fatalf("navigation: status = %d, want 302", rec.Code)
	}
	if !strings.HasPrefix(rec.Header().Get("Location"), "/admin/verify-totp?next=") {
		t.Errorf("Location = %q, want verify redirect", rec.Header().Get("Location"))
	}

	xhr := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounting/x", nil)
	xhr.Header.Set("Accept", "application/json")
	xrec := httptest.NewRecorder()
	writeTOTPFreshRequired(xrec, xhr, 5*time.Minute)
	if xrec.Code != http.StatusForbidden {
		t.Fatalf("xhr: status = %d, want 403", xrec.Code)
	}
	if !strings.Contains(xrec.Body.String(), `"totp_fresh_required"`) {
		t.Errorf("xhr: body = %q, want totp_fresh_required JSON", xrec.Body.String())
	}
}

func nextParam(t *testing.T, location string) string {
	t.Helper()
	u, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse Location %q: %v", location, err)
	}
	return u.Query().Get("next")
}
