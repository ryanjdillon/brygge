package handlers

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
)

// An admin whose TOTP an admin disables can no longer mint tokens, so the
// tokens they hold are revoked with it.
func TestIntegration_AdminDisableTOTPRevokesDeveloperTokens(t *testing.T) {
	t.Parallel()
	e := newGuardEnv(t)
	target := e.seedAdmin(t)
	e.seedDevToken(t, target)
	other := e.seedAdmin(t)
	e.seedDevToken(t, other)

	h := NewTOTPHandler(e.db, testConfig(), nil, nil, zerolog.Nop())
	r := chi.NewRouter()
	r.Use(testAuthMiddleware)
	r.Post("/admin/users/{userID}/totp/disable", h.HandleAdminDisableTOTP)
	e.router = r

	assertStatus(t, e.call(e.admin, []string{"admin"}, http.MethodPost, "/admin/users/"+target+"/totp/disable", ""), http.StatusOK)
	if n := e.activeTokens(t, target); n != 0 {
		t.Errorf("target's tokens active after TOTP disable = %d, want 0", n)
	}
	if n := e.activeTokens(t, other); n != 1 {
		t.Errorf("another admin's tokens must be untouched, active = %d, want 1", n)
	}
}
