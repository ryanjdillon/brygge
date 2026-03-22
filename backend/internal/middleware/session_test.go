package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
