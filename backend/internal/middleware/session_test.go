package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brygge-klubb/brygge/internal/auth"
)

func TestAuthenticateSessionMissingCookie(t *testing.T) {
	// nil sessionService — we shouldn't reach it because cookie is missing
	handler := AuthenticateSession(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called without session cookie")
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestOptionalSessionAuthMissingCookie(t *testing.T) {
	called := false
	handler := OptionalSessionAuth(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		claims := GetClaims(r.Context())
		if claims != nil {
			t.Error("expected nil claims without cookie")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("handler should be called for optional auth")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, rec.Code)
	}
}

func TestSetAndClearSessionCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	SetSessionCookie(rec, "test-session-id", true)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}

	c := cookies[0]
	if c.Name != sessionCookieName {
		t.Errorf("cookie name = %q, want %q", c.Name, sessionCookieName)
	}
	if c.Value != "test-session-id" {
		t.Errorf("cookie value = %q, want %q", c.Value, "test-session-id")
	}
	if !c.HttpOnly {
		t.Error("expected HttpOnly cookie")
	}
	if !c.Secure {
		t.Error("expected Secure cookie")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}

	// Clear
	rec2 := httptest.NewRecorder()
	ClearSessionCookie(rec2)
	cookies2 := rec2.Result().Cookies()
	if len(cookies2) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies2))
	}
	if cookies2[0].MaxAge != -1 {
		t.Errorf("MaxAge = %d, want -1 (delete)", cookies2[0].MaxAge)
	}
}

func TestGetSessionIDFromContext(t *testing.T) {
	// No session in context
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	id := GetSessionID(req.Context())
	if id != "" {
		t.Errorf("expected empty session ID, got %q", id)
	}
}

// withSessionInfo is a test-only injector that puts SessionInfo into
// context the same way AuthenticateSession does in production.
func withSessionInfo(ctx context.Context, info *auth.SessionInfo) context.Context {
	return context.WithValue(ctx, sessionInfoContextKey{}, info)
}

func runRequireAdminTOTP(t *testing.T, info *auth.SessionInfo) *httptest.ResponseRecorder {
	t.Helper()
	called := false
	handler := RequireAdminTOTP(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/test", nil)
	req = req.WithContext(withSessionInfo(req.Context(), info))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK && !called {
		t.Fatal("status 200 but downstream handler not called")
	}
	return rec
}

func TestRequireAdminTOTPNotEnrolled(t *testing.T) {
	rec := runRequireAdminTOTP(t, &auth.SessionInfo{TOTPEnabled: false})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "totp_not_enrolled" {
		t.Errorf("error = %q, want totp_not_enrolled", body["error"])
	}
}

func TestRequireAdminTOTPNeverVerified(t *testing.T) {
	rec := runRequireAdminTOTP(t, &auth.SessionInfo{TOTPEnabled: true, TOTPVerifiedAt: nil})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["error"] != "totp_required" {
		t.Errorf("error = %q, want totp_required", body["error"])
	}
}

func TestRequireAdminTOTPFreshAllowed(t *testing.T) {
	verified := time.Now().Add(-11 * time.Hour)
	rec := runRequireAdminTOTP(t, &auth.SessionInfo{TOTPEnabled: true, TOTPVerifiedAt: &verified})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestRequireAdminTOTPStaleRejected(t *testing.T) {
	verified := time.Now().Add(-13 * time.Hour)
	rec := runRequireAdminTOTP(t, &auth.SessionInfo{TOTPEnabled: true, TOTPVerifiedAt: &verified})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["error"] != "totp_required" {
		t.Errorf("error = %q, want totp_required", body["error"])
	}
}

func TestGetSessionInfoMissing(t *testing.T) {
	if got := GetSessionInfo(context.Background()); got != nil {
		t.Errorf("GetSessionInfo on bare ctx = %v, want nil", got)
	}
}

func runRequireFreshTOTP(t *testing.T, info *auth.SessionInfo, window time.Duration) *httptest.ResponseRecorder {
	t.Helper()
	called := false
	handler := RequireFreshTOTP(window)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPut, "/admin/users/x/roles", nil)
	req = req.WithContext(withSessionInfo(req.Context(), info))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK && !called {
		t.Fatal("status 200 but downstream handler not called")
	}
	return rec
}

func TestRequireFreshTOTPFreshAllowed(t *testing.T) {
	verified := time.Now().Add(-2 * time.Minute)
	rec := runRequireFreshTOTP(t, &auth.SessionInfo{TOTPEnabled: true, TOTPVerifiedAt: &verified}, 5*time.Minute)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestRequireFreshTOTPStaleRejected(t *testing.T) {
	verified := time.Now().Add(-6 * time.Minute)
	rec := runRequireFreshTOTP(t, &auth.SessionInfo{TOTPEnabled: true, TOTPVerifiedAt: &verified}, 5*time.Minute)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	var body map[string]any
	json.NewDecoder(rec.Body).Decode(&body)
	if body["error"] != "totp_fresh_required" {
		t.Errorf("error = %v, want totp_fresh_required", body["error"])
	}
	if window, _ := body["window_seconds"].(float64); window != 300 {
		t.Errorf("window_seconds = %v, want 300", body["window_seconds"])
	}
}

func TestRequireFreshTOTPNotEnrolled(t *testing.T) {
	rec := runRequireFreshTOTP(t, &auth.SessionInfo{TOTPEnabled: false}, 5*time.Minute)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestRequireFreshTOTPNoSessionInfo(t *testing.T) {
	handler := RequireFreshTOTP(5 * time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run without session info")
	}))
	req := httptest.NewRequest(http.MethodPut, "/admin/users/x/roles", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestIsFreshTOTPHandlerLevel(t *testing.T) {
	verified := time.Now().Add(-30 * time.Second)
	ctx := withSessionInfo(context.Background(), &auth.SessionInfo{TOTPEnabled: true, TOTPVerifiedAt: &verified})
	if !IsFreshTOTP(ctx, 5*time.Minute) {
		t.Error("expected fresh within 5min window")
	}
	if IsFreshTOTP(ctx, 10*time.Second) {
		t.Error("expected stale outside 10s window")
	}
}
