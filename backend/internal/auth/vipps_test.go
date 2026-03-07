package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestVippsClient(baseURL string) *VippsClient {
	return &VippsClient{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		CallbackURL:  "https://example.com/callback",
		BaseURL:      baseURL,
		BrowserURL:   baseURL,
		HTTPClient:   http.DefaultClient,
	}
}

func TestAuthorizationURL(t *testing.T) {
	client := newTestVippsClient("https://apitest.vipps.no")
	state := "random-state-value"

	url := client.AuthorizationURL(state)

	checks := []struct {
		name     string
		contains string
	}{
		{"client_id", "client_id=test-client-id"},
		{"scope", "scope="},
		{"state", "state=random-state-value"},
		{"redirect_uri", "redirect_uri="},
		{"response_type", "response_type=code"},
	}

	for _, c := range checks {
		if !strings.Contains(url, c.contains) {
			t.Errorf("URL missing %s: got %s", c.name, url)
		}
	}
}

func TestAuthorizationURLTestMode(t *testing.T) {
	client := newTestVippsClient("https://apitest.vipps.no")
	url := client.AuthorizationURL("state")

	if !strings.HasPrefix(url, "https://apitest.vipps.no") {
		t.Errorf("test mode URL = %q, want prefix https://apitest.vipps.no", url)
	}
}

func TestAuthorizationURLProdMode(t *testing.T) {
	client := newTestVippsClient("https://api.vipps.no")
	url := client.AuthorizationURL("state")

	if !strings.HasPrefix(url, "https://api.vipps.no") {
		t.Errorf("prod mode URL = %q, want prefix https://api.vipps.no", url)
	}
}

func TestAuthorizationURLUsesBrowserURL(t *testing.T) {
	client := &VippsClient{
		ClientID:   "test",
		BaseURL:    "http://vipps-mock:8090",
		BrowserURL: "http://localhost:8090",
		CallbackURL: "http://localhost:8080/callback",
	}
	url := client.AuthorizationURL("state")
	if !strings.HasPrefix(url, "http://localhost:8090") {
		t.Errorf("URL = %q, want prefix http://localhost:8090", url)
	}
}

func TestEnabled(t *testing.T) {
	tests := []struct {
		name    string
		client  *VippsClient
		enabled bool
	}{
		{"mock mode", &VippsClient{BaseURL: "http://vipps-mock:8090"}, true},
		{"localhost mock", &VippsClient{BaseURL: "http://localhost:8090"}, true},
		{"with credentials", &VippsClient{ClientID: "id", ClientSecret: "secret", BaseURL: "https://apitest.vipps.no"}, true},
		{"no credentials", &VippsClient{BaseURL: "https://apitest.vipps.no"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.client.Enabled(); got != tt.enabled {
				t.Errorf("Enabled() = %v, want %v", got, tt.enabled)
			}
		})
	}
}

func TestExchangeCode(t *testing.T) {
	wantResponse := VippsTokenResponse{
		AccessToken:  "mock-access-token",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		RefreshToken: "mock-refresh-token",
		IDToken:      "mock-id-token",
		Scope:        "openid",
	}

	var gotContentType string
	var gotAuthHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(wantResponse)
	}))
	defer server.Close()

	client := &VippsClient{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		CallbackURL:  "https://example.com/callback",
		BaseURL:      server.URL,
		BrowserURL:   server.URL,
		HTTPClient:   server.Client(),
	}

	resp, err := client.ExchangeCode(context.Background(), "auth-code-123")
	if err != nil {
		t.Fatalf("ExchangeCode() error = %v", err)
	}

	if gotContentType != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want %q", gotContentType, "application/x-www-form-urlencoded")
	}
	if gotAuthHeader == "" {
		t.Error("Authorization header missing, expected Basic auth")
	}
	if !strings.HasPrefix(gotAuthHeader, "Basic ") {
		t.Errorf("Authorization = %q, want Basic auth prefix", gotAuthHeader)
	}
	if resp.AccessToken != wantResponse.AccessToken {
		t.Errorf("AccessToken = %q, want %q", resp.AccessToken, wantResponse.AccessToken)
	}
	if resp.RefreshToken != wantResponse.RefreshToken {
		t.Errorf("RefreshToken = %q, want %q", resp.RefreshToken, wantResponse.RefreshToken)
	}
}

func TestExchangeCodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer server.Close()

	client := &VippsClient{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		CallbackURL:  "https://example.com/callback",
		BaseURL:      server.URL,
		BrowserURL:   server.URL,
		HTTPClient:   server.Client(),
	}

	_, err := client.ExchangeCode(context.Background(), "bad-code")
	if err == nil {
		t.Fatal("ExchangeCode() expected error for 400 response, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error = %q, want mention of status 400", err.Error())
	}
}

func TestGetUserInfo(t *testing.T) {
	wantInfo := VippsUserInfo{
		Sub:   "vipps-sub-123",
		Name:  "Ola Nordmann",
		Email: "ola@example.com",
		Phone: "+4712345678",
		Address: VippsAddress{
			Street:     "Storgata 1",
			PostalCode: "0001",
			City:       "Oslo",
			Country:    "NO",
		},
	}

	var gotAuthHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(wantInfo)
	}))
	defer server.Close()

	client := &VippsClient{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		CallbackURL:  "https://example.com/callback",
		BaseURL:      server.URL,
		BrowserURL:   server.URL,
		HTTPClient:   server.Client(),
	}

	info, err := client.GetUserInfo(context.Background(), "my-access-token")
	if err != nil {
		t.Fatalf("GetUserInfo() error = %v", err)
	}

	if gotAuthHeader != "Bearer my-access-token" {
		t.Errorf("Authorization = %q, want %q", gotAuthHeader, "Bearer my-access-token")
	}
	if info.Sub != wantInfo.Sub {
		t.Errorf("Sub = %q, want %q", info.Sub, wantInfo.Sub)
	}
	if info.Name != wantInfo.Name {
		t.Errorf("Name = %q, want %q", info.Name, wantInfo.Name)
	}
	if info.Email != wantInfo.Email {
		t.Errorf("Email = %q, want %q", info.Email, wantInfo.Email)
	}
}
