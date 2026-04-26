package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
)

func TestGenerateRecoveryCodesShape(t *testing.T) {
	codes, err := generateRecoveryCodes(10)
	if err != nil {
		t.Fatalf("generateRecoveryCodes: %v", err)
	}
	if len(codes) != 10 {
		t.Fatalf("got %d codes, want 10", len(codes))
	}
	seen := make(map[string]bool, 10)
	for i, c := range codes {
		if len(c) != 9 {
			t.Errorf("code %d %q: length %d, want 9", i, c, len(c))
		}
		if c[4] != '-' {
			t.Errorf("code %d %q: missing dash at position 4", i, c)
		}
		// Each glyph must be in the safe alphabet (no 0/O/1/I/L).
		for _, r := range strings.ReplaceAll(c, "-", "") {
			if !strings.ContainsRune(recoveryAlphabet, r) {
				t.Errorf("code %d %q: char %q not in safe alphabet", i, c, r)
			}
		}
		if seen[c] {
			t.Errorf("duplicate code %q", c)
		}
		seen[c] = true
	}
}

func TestHandleAdminDisableTOTPUnauthenticated(t *testing.T) {
	h := NewTOTPHandler(nil, testConfig(), nil, nil, zerolog.Nop())

	// No auth wrapper — handler should reject for missing claims.
	r := chi.NewRouter()
	r.Post("/admin/users/{userID}/totp/disable", h.HandleAdminDisableTOTP)

	req := httptest.NewRequest(http.MethodPost, "/admin/users/abc/totp/disable", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected %d, got %d (body: %s)", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleAdminDisableTOTPMissingUserID(t *testing.T) {
	h := NewTOTPHandler(nil, testConfig(), nil, nil, zerolog.Nop())

	// Wrap with the test auth middleware so claims are present, but
	// don't bind the URL param — the handler should fall into the
	// "userID required" branch.
	r := chi.NewRouter()
	r.Use(testAuthMiddleware)
	r.Post("/disable", h.HandleAdminDisableTOTP)

	token := generateTestToken("admin-1", "club-1", []string{"admin"})
	req := httptest.NewRequest(http.MethodPost, "/disable", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d (body: %s)", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestNormalizeRecoveryCode(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"ABCD-EFGH", "ABCD-EFGH"},
		{"abcd-efgh", "ABCD-EFGH"},
		{"ABCDEFGH", "ABCD-EFGH"},
		{" ABCD-EFGH ", "ABCD-EFGH"},
		{"abcd efgh", "ABCD-EFGH"},
		{"ABCD-EFG", ""},  // too short
		{"ABCD-EFGHI", ""}, // too long
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeRecoveryCode(tt.in)
		if got != tt.want {
			t.Errorf("normalizeRecoveryCode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
