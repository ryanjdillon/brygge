package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brygge-klubb/brygge/internal/auth"
)

func TestApplicantMayReach(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{http.MethodGet, "/api/v1/members/me", true},
		{http.MethodPut, "/api/v1/members/me", true},
		{http.MethodGet, "/api/v1/members/me/invoices/11111111-1111-1111-1111-111111111111/pdf", true},
		{http.MethodPost, "/api/v1/members/me/consent", true},
		{http.MethodPost, "/api/v1/members/me/delete-request", true},
		{http.MethodPost, "/api/v1/auth/session/logout", true},
		{http.MethodPost, "/api/v1/auth/passkeys/step-up/begin", true},
		// The method matters: a listed path is not open to every method.
		{http.MethodDelete, "/api/v1/members/me", false},
		{http.MethodGet, "/api/v1/members/directory", false},
		{http.MethodGet, "/api/v1/members/me/slip", false},
		{http.MethodGet, "/api/v1/documents", false},
		{http.MethodPost, "/api/v1/waiting-list/join", false},
		{http.MethodGet, "/api/v1/portal/slip-shares", false},
		{http.MethodGet, "/api/v1/admin/users", false},
		// Patterns match whole paths, not prefixes.
		{http.MethodGet, "/api/v1/members/meta", false},
		{http.MethodGet, "/api/v1/members/me/invoices/x/pdf/extra", false},
	} {
		if got := ApplicantMayReach(tc.method, tc.path); got != tc.want {
			t.Errorf("ApplicantMayReach(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestRefuseApplicant(t *testing.T) {
	for _, tc := range []struct {
		name    string
		roles   []string
		path    string
		refused bool
	}{
		{"applicant off the allowlist", []string{"applicant"}, "/api/v1/members/directory", true},
		{"applicant on the allowlist", []string{"applicant"}, "/api/v1/members/me", false},
		{"member", []string{"member"}, "/api/v1/members/directory", false},
		{"applicant who is also on the board", []string{"applicant", "board"}, "/api/v1/members/directory", false},
		{"no roles", nil, "/api/v1/members/directory", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			got := refuseApplicant(rec, req, &auth.Claims{UserID: "u", ClubID: "c", Roles: tc.roles})
			if got != tc.refused {
				t.Fatalf("refused = %v, want %v", got, tc.refused)
			}
			if !tc.refused {
				return
			}
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rec.Code)
			}
			var body map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || body["error"] != ErrMembershipNotCompleted {
				t.Errorf("body = %v (%v), want error %q", body, err, ErrMembershipNotCompleted)
			}
		})
	}
}

// The gate decides on the path chi routes on: the escaped form when the
// request has one. An encoded "/" stays inside one segment for both.
func TestRefuseApplicantUsesTheRoutingPath(t *testing.T) {
	claims := &auth.Claims{UserID: "u", ClubID: "c", Roles: []string{"applicant"}}
	for _, tc := range []struct {
		target  string
		refused bool
	}{
		// chi routes this to the PDF route with invoiceID "x%2Fy"; the decoded
		// path has an extra segment and matches nothing on the allowlist.
		{"/api/v1/members/me/invoices/x%2Fy/pdf", false},
		// Decoded it reads /members/me/consents, but chi sees one segment
		// "me%2Fconsents", which is no allowlisted route.
		{"/api/v1/members/me%2Fconsents", true},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.target, nil)
		if req.URL.RawPath == "" {
			t.Fatalf("%s: test request has no RawPath", tc.target)
		}
		if got := refuseApplicant(httptest.NewRecorder(), req, claims); got != tc.refused {
			t.Errorf("%s: refused = %v, want %v", tc.target, got, tc.refused)
		}
	}
}
