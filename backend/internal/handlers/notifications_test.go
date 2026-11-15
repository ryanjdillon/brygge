package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func newTestNotificationsHandler(t *testing.T) *NotificationsHandler {
	t.Helper()
	cfg := testConfig()
	cfg.VAPIDPublicKey = "test-vapid-public-key"
	cfg.VAPIDPrivateKey = "test-vapid-private-key"
	log := zerolog.Nop()
	return NewNotificationsHandler(nil, cfg, log)
}

func TestHandleGetVAPIDKey(t *testing.T) {
	h := newTestNotificationsHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/push/vapid-key", nil)
	rec := httptest.NewRecorder()

	h.HandleGetVAPIDKey(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %s", ct)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["vapid_key"] != "test-vapid-public-key" {
		t.Errorf("expected vapid_key 'test-vapid-public-key', got %q", resp["vapid_key"])
	}
}

func TestHandleGetVAPIDKeyEmpty(t *testing.T) {
	cfg := testConfig()
	log := zerolog.Nop()
	h := NewNotificationsHandler(nil, cfg, log)

	req := httptest.NewRequest(http.MethodGet, "/push/vapid-key", nil)
	rec := httptest.NewRecorder()

	h.HandleGetVAPIDKey(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["vapid_key"] != "" {
		t.Errorf("expected empty vapid_key, got %q", resp["vapid_key"])
	}
}

func TestHandleSubscribeUnauthenticated(t *testing.T) {
	h := newTestNotificationsHandler(t)
	r := setupAuthenticatedRouter(http.MethodPost, "/push/subscribe", h.HandleSubscribe)

	body := `{"endpoint":"https://push.example.com/sub/123","keys":{"p256dh":"key1","auth":"key2"}}`
	req := httptest.NewRequest(http.MethodPost, "/push/subscribe", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusUnauthorized, rec.Code, rec.Body.String())
	}
}

func TestHandleSubscribeInvalidBody(t *testing.T) {
	h := newTestNotificationsHandler(t)
	r := setupAuthenticatedRouter(http.MethodPost, "/push/subscribe", h.HandleSubscribe)

	token := generateTestToken("user-123", "club-456", []string{"member"})

	req := httptest.NewRequest(http.MethodPost, "/push/subscribe", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleSubscribeMissingFields(t *testing.T) {
	h := newTestNotificationsHandler(t)
	r := setupAuthenticatedRouter(http.MethodPost, "/push/subscribe", h.HandleSubscribe)

	token := generateTestToken("user-123", "club-456", []string{"member"})

	tests := []struct {
		name string
		body string
	}{
		{"missing endpoint", `{"keys":{"p256dh":"key1","auth":"key2"}}`},
		{"missing p256dh", `{"endpoint":"https://push.example.com","keys":{"auth":"key2"}}`},
		{"missing auth", `{"endpoint":"https://push.example.com","keys":{"p256dh":"key1"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/push/subscribe", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", authHeader(token))
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandleUnsubscribeUnauthenticated(t *testing.T) {
	h := newTestNotificationsHandler(t)
	r := setupAuthenticatedRouter(http.MethodDelete, "/push/subscribe", h.HandleUnsubscribe)

	body := `{"endpoint":"https://push.example.com/sub/123"}`
	req := httptest.NewRequest(http.MethodDelete, "/push/subscribe", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestHandleUnsubscribeMissingEndpoint(t *testing.T) {
	h := newTestNotificationsHandler(t)
	r := setupAuthenticatedRouter(http.MethodDelete, "/push/subscribe", h.HandleUnsubscribe)

	token := generateTestToken("user-123", "club-456", []string{"member"})

	req := httptest.NewRequest(http.MethodDelete, "/push/subscribe", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleGetPreferencesUnauthenticated(t *testing.T) {
	h := newTestNotificationsHandler(t)
	r := setupAuthenticatedRouter(http.MethodGet, "/members/me/notifications", h.HandleGetPreferences)

	req := httptest.NewRequest(http.MethodGet, "/members/me/notifications", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestHandleUpdatePreferencesInvalidBody(t *testing.T) {
	h := newTestNotificationsHandler(t)
	r := setupAuthenticatedRouter(http.MethodPut, "/members/me/notifications", h.HandleUpdatePreferences)

	token := generateTestToken("user-123", "club-456", []string{"member"})

	req := httptest.NewRequest(http.MethodPut, "/members/me/notifications", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleUpdatePreferencesMissingCategory(t *testing.T) {
	h := newTestNotificationsHandler(t)
	r := setupAuthenticatedRouter(http.MethodPut, "/members/me/notifications", h.HandleUpdatePreferences)

	token := generateTestToken("user-123", "club-456", []string{"member"})

	req := httptest.NewRequest(http.MethodPut, "/members/me/notifications", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Error != "category is required" {
		t.Errorf("expected error 'category is required', got %q", resp.Error)
	}
}

func TestHandleUpdatePreferencesUnknownCategory(t *testing.T) {
	h := newTestNotificationsHandler(t)
	r := setupAuthenticatedRouter(http.MethodPut, "/members/me/notifications", h.HandleUpdatePreferences)

	token := generateTestToken("user-123", "club-456", []string{"member"})

	req := httptest.NewRequest(http.MethodPut, "/members/me/notifications", strings.NewReader(`{"category":"nonexistent","enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}

	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Error != "unknown category" {
		t.Errorf("expected error 'unknown category', got %q", resp.Error)
	}
}

func TestHandleUpdateConfigUnauthenticated(t *testing.T) {
	h := newTestNotificationsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPut, "/admin/notifications/config", h.HandleUpdateConfig, "board", "admin")

	body := `{"category":"payment_reminder","required":true}`
	req := httptest.NewRequest(http.MethodPut, "/admin/notifications/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, rec.Code)
	}
}

func TestHandleUpdateConfigForbidden(t *testing.T) {
	h := newTestNotificationsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPut, "/admin/notifications/config", h.HandleUpdateConfig, "board", "admin")

	token := generateTestToken("user-123", "club-456", []string{"member"})

	body := `{"category":"payment_reminder","required":true}`
	req := httptest.NewRequest(http.MethodPut, "/admin/notifications/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusForbidden, rec.Code, rec.Body.String())
	}
}

func TestHandleUpdateConfigUnknownCategory(t *testing.T) {
	h := newTestNotificationsHandler(t)
	r := setupRoleProtectedRouter(http.MethodPut, "/admin/notifications/config", h.HandleUpdateConfig, "board", "admin")

	token := generateTestToken("user-123", "club-456", []string{"board"})

	body := `{"category":"nonexistent","required":true}`
	req := httptest.NewRequest(http.MethodPut, "/admin/notifications/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleTestPushNoVAPIDKeys(t *testing.T) {
	cfg := testConfig()
	log := zerolog.Nop()
	h := NewNotificationsHandler(nil, cfg, log)

	r := setupAuthenticatedRouter(http.MethodPost, "/notifications/test", h.HandleTestPush)

	token := generateTestToken("user-123", "club-456", []string{"member"})

	req := httptest.NewRequest(http.MethodPost, "/notifications/test", nil)
	req.Header.Set("Authorization", authHeader(token))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d; body: %s", http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["code"] != codePushNotConfigured {
		t.Errorf("expected code %q, got %q", codePushNotConfigured, resp["code"])
	}
}

func TestVAPIDSubscriberIsNotDoubleMailto(t *testing.T) {
	cfg := testConfig()
	cfg.FrontendURL = "https://club.example.invalid"
	if got := vapidSubscriber(cfg); got != "https://club.example.invalid" {
		t.Errorf("https site: got %q", got)
	}
	cfg.FrontendURL = "http://localhost:5173"
	cfg.Domain = "localhost"
	got := vapidSubscriber(cfg)
	if strings.HasPrefix(got, "mailto:") {
		t.Errorf("subscriber %q must not carry mailto: (webpush-go adds it)", got)
	}
}

func TestDefaultCategories(t *testing.T) {
	if len(defaultCategories) != 8 {
		t.Fatalf("expected 8 default categories, got %d", len(defaultCategories))
	}

	expected := map[string]bool{
		"payment_reminder":   true,
		"slip_offer":         true,
		"booking_confirm":    true,
		"volunteer_reminder": true,
		"board_announcement": true,
		"waiting_list":       true,
		"new_document":       false,
		"event_reminder":     true,
	}

	for _, dc := range defaultCategories {
		want, ok := expected[dc.Name]
		if !ok {
			t.Errorf("unexpected category %q", dc.Name)
			continue
		}
		if dc.Default != want {
			t.Errorf("category %q: expected default=%v, got %v", dc.Name, want, dc.Default)
		}
	}
}
