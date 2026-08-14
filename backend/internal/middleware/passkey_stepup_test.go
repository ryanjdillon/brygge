package middleware

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/auth"
)

// A passkey (no TOTP) with a recent step-up satisfies the admin gate.
func TestRequireAdminTOTPPasskeySatisfies(t *testing.T) {
	verified := time.Now().Add(-1 * time.Hour)
	rec := runRequireAdminTOTP(t, &auth.SessionInfo{TOTPEnabled: false, HasPasskey: true, TOTPVerifiedAt: &verified})
	if rec.Code != http.StatusOK {
		t.Fatalf("passkey step-up should satisfy the admin gate; status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

// A passkey whose step-up has gone stale is rejected like a stale TOTP.
func TestRequireAdminTOTPPasskeyStaleRejected(t *testing.T) {
	verified := time.Now().Add(-13 * time.Hour)
	rec := runRequireAdminTOTP(t, &auth.SessionInfo{TOTPEnabled: false, HasPasskey: true, TOTPVerifiedAt: &verified})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// No enrolled factor at all is rejected as not-enrolled.
func TestRequireAdminTOTPNoFactorRejected(t *testing.T) {
	rec := runRequireAdminTOTP(t, &auth.SessionInfo{TOTPEnabled: false, HasPasskey: false})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// IsFreshTOTP accepts a recent passkey step-up even without TOTP...
func TestIsFreshTOTPPasskeySatisfies(t *testing.T) {
	verified := time.Now().Add(-2 * time.Minute)
	ctx := withSessionInfo(context.Background(), &auth.SessionInfo{TOTPEnabled: false, HasPasskey: true, TOTPVerifiedAt: &verified})
	if !IsFreshTOTP(ctx, 10*time.Minute) {
		t.Error("a recent passkey step-up should be fresh")
	}
}

// ...but a session with no enrolled factor is never fresh, even if the
// timestamp is somehow set.
func TestIsFreshTOTPNoFactorNeverFresh(t *testing.T) {
	verified := time.Now()
	ctx := withSessionInfo(context.Background(), &auth.SessionInfo{TOTPEnabled: false, HasPasskey: false, TOTPVerifiedAt: &verified})
	if IsFreshTOTP(ctx, 10*time.Minute) {
		t.Error("no enrolled factor must never be considered fresh")
	}
}
