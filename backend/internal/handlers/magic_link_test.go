package handlers

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
)

func newTestMagicLinkHandler(t *testing.T) (*MagicLinkHandler, *email.MockSender) {
	t.Helper()
	cfg := testConfig()
	cfg.FrontendURL = "http://localhost:5173"
	log := zerolog.Nop()
	mock := &email.MockSender{}
	h := NewMagicLinkHandler(nil, cfg, mock, nil, nil, log)
	return h, mock
}

func TestHandleRequestMagicLinkMissingEmail(t *testing.T) {
	h, _ := newTestMagicLinkHandler(t)

	r := chi.NewRouter()
	r.Post("/auth/magic-link", h.HandleRequestMagicLink)

	req := httptest.NewRequest(http.MethodPost, "/auth/magic-link", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleRequestMagicLinkInvalidBody(t *testing.T) {
	h, _ := newTestMagicLinkHandler(t)

	r := chi.NewRouter()
	r.Post("/auth/magic-link", h.HandleRequestMagicLink)

	req := httptest.NewRequest(http.MethodPost, "/auth/magic-link", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestHandleVerifyMagicLinkNoEmailSender(t *testing.T) {
	cfg := testConfig()
	cfg.FrontendURL = "http://localhost:5173"
	log := zerolog.Nop()
	// nil email sender — handler should still work
	h := NewMagicLinkHandler(nil, cfg, nil, nil, nil, log)

	r := chi.NewRouter()
	r.Post("/auth/magic-link", h.HandleRequestMagicLink)

	// With nil db this will panic on QueryRow, but missing email returns 400 before hitting db
	body := `{"email":""}`
	req := httptest.NewRequest(http.MethodPost, "/auth/magic-link", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

// The GET an old login link makes consumes nothing (it needs no database)
// and sends the browser to the SPA page with the same token.
func TestHandleVerifyMagicLinkRedirectsToLoginPage(t *testing.T) {
	h, _ := newTestMagicLinkHandler(t)
	h.config.FrontendURL = "https://club.example.test"

	r := chi.NewRouter()
	r.Get("/auth/verify", h.HandleVerifyMagicLink)

	for _, c := range []struct{ target, want string }{
		{"/auth/verify?token=abc_DEF-123", "https://club.example.test/login/verify?token=abc_DEF-123"},
		{"/auth/verify", "https://club.example.test/login/verify"},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.target, nil))
		if rec.Code != http.StatusFound {
			t.Fatalf("%s: status %d, want 302", c.target, rec.Code)
		}
		if got := rec.Header().Get("Location"); got != c.want {
			t.Errorf("%s: Location %q, want %q", c.target, got, c.want)
		}
		if rec.Header().Get("Set-Cookie") != "" {
			t.Errorf("%s: the redirect set a cookie", c.target)
		}
	}
}

// Only a JSON body may log in: a cross-site form cannot send one, so this is
// the login-CSRF guard. It answers before any database access.
func TestHandleConsumeMagicLinkRequiresJSON(t *testing.T) {
	h, _ := newTestMagicLinkHandler(t)

	r := chi.NewRouter()
	r.Post("/auth/verify", h.HandleConsumeMagicLink)

	for _, ct := range []string{"", "application/x-www-form-urlencoded", "text/plain", "multipart/form-data; boundary=x"} {
		req := httptest.NewRequest(http.MethodPost, "/auth/verify", strings.NewReader(`{"token":"abc"}`))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %q: status %d, want 415", ct, rec.Code)
		}
	}
}

func TestHandleConsumeMagicLinkMissingToken(t *testing.T) {
	h, _ := newTestMagicLinkHandler(t)

	r := chi.NewRouter()
	r.Post("/auth/verify", h.HandleConsumeMagicLink)

	req := httptest.NewRequest(http.MethodPost, "/auth/verify", strings.NewReader(`{"token":"  "}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusGone {
		t.Fatalf("expected %d, got %d", http.StatusGone, rec.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["code"] != codeLoginTokenInvalid {
		t.Errorf("code = %q, want %q", resp["code"], codeLoginTokenInvalid)
	}
}

func TestLoginVerifyURL(t *testing.T) {
	cfg := &config.Config{FrontendURL: "https://club.example.test/"}
	if got := loginVerifyURL(cfg, "a+b/c"); got != "https://club.example.test/login/verify?token=a%2Bb%2Fc" {
		t.Errorf("loginVerifyURL = %q", got)
	}
}

// NOTE: TestHandleVerifyMagicLinkInvalidToken and TestHandleRequestMagicLinkUnknownEmail
// require a real database (pgx panics on nil pool). These are covered by integration tests.
// Unit tests here cover validation paths that don't hit the DB.

func TestGenerateToken(t *testing.T) {
	token1, err := generateToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be base64url encoded, 32 bytes → 43 chars without padding
	if len(token1) != 43 {
		t.Errorf("token length = %d, want 43", len(token1))
	}

	// Should decode to 32 bytes
	decoded, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(token1)
	if err != nil {
		t.Fatalf("failed to decode token: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("decoded length = %d, want 32", len(decoded))
	}

	// Two tokens should be different
	token2, err := generateToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token1 == token2 {
		t.Error("two generated tokens should not be equal")
	}
}

func TestCookieSecure(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{"https origin", &config.Config{FrontendURL: "https://club.example"}, true},
		{"http origin", &config.Config{FrontendURL: "http://localhost:5173"}, false},
		{"empty origin", &config.Config{}, false},
		{"nil config", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cookieSecure(tt.cfg); got != tt.want {
				t.Errorf("cookieSecure() = %v, want %v", got, tt.want)
			}
		})
	}
}
