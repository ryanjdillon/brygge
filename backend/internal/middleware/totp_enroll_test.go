package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/auth"
)

func runRequireFreshTOTPIfEnrolled(t *testing.T, info *auth.SessionInfo) *httptest.ResponseRecorder {
	t.Helper()
	h := RequireFreshTOTPIfEnrolled()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/totp/setup", nil)
	if info != nil {
		req = req.WithContext(withSessionInfo(context.Background(), info))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRequireFreshTOTPIfEnrolled(t *testing.T) {
	fresh := time.Now().Add(-2 * time.Minute)
	stale := time.Now().Add(-30 * time.Minute)

	cases := []struct {
		name string
		info *auth.SessionInfo
		want int
	}{
		// First enrollment: no factor yet, so the session is the only proof.
		{"no factor, never stepped up", &auth.SessionInfo{}, http.StatusOK},

		// Replacing a factor on a stale or never-verified session is the
		// stolen-cookie case and must be refused.
		{"TOTP enrolled, stale step-up", &auth.SessionInfo{TOTPEnabled: true, TOTPVerifiedAt: &stale}, http.StatusForbidden},
		{"TOTP enrolled, never stepped up", &auth.SessionInfo{TOTPEnabled: true}, http.StatusForbidden},
		{"passkey only, stale step-up", &auth.SessionInfo{HasPasskey: true, TOTPVerifiedAt: &stale}, http.StatusForbidden},

		// A fresh step-up with either factor lets the owner re-enroll.
		{"TOTP enrolled, fresh step-up", &auth.SessionInfo{TOTPEnabled: true, TOTPVerifiedAt: &fresh}, http.StatusOK},
		{"passkey only, fresh step-up", &auth.SessionInfo{HasPasskey: true, TOTPVerifiedAt: &fresh}, http.StatusOK},

		// Without session info we can't tell, so refuse.
		{"no session info", nil, http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := runRequireFreshTOTPIfEnrolled(t, c.info)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, c.want, rec.Body.String())
			}
			if c.want == http.StatusForbidden && !strings.Contains(rec.Body.String(), "totp_fresh_required") {
				t.Errorf("403 should be the fresh step-up error so the SPA opens its modal; body = %s", rec.Body.String())
			}
		})
	}
}
