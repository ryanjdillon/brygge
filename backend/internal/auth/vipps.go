package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/brygge-klubb/brygge/internal/config"
)

const (
	vippsAuthPath     = "/access-management-1.0/access/oauth2/auth"
	vippsTokenPath    = "/access-management-1.0/access/oauth2/token"
	vippsUserInfoPath = "/vipps-userinfo-api/userinfo"

	vippsScopes = "openid address name email phoneNumber"
)

type VippsAddress struct {
	Street     string `json:"street_address"`
	PostalCode string `json:"postal_code"`
	City       string `json:"region"`
	Country    string `json:"country"`
}

type VippsUserInfo struct {
	Sub     string       `json:"sub"`
	Name    string       `json:"name"`
	Email   string       `json:"email"`
	Phone   string       `json:"phone_number"`
	Address VippsAddress `json:"address"`
}

type VippsTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	Scope        string `json:"scope"`
}

type VippsClient struct {
	ClientID        string
	ClientSecret    string
	CallbackURL     string
	MSN             string
	SubscriptionKey string
	BaseURL         string
	BrowserURL      string
	HTTPClient      *http.Client
}

func NewVippsClient(cfg *config.Config) *VippsClient {
	return &VippsClient{
		ClientID:        cfg.VippsClientID,
		ClientSecret:    cfg.VippsClientSecret,
		CallbackURL:     cfg.VippsCallbackURL,
		MSN:             cfg.VippsMSN,
		SubscriptionKey: cfg.VippsSubscriptionKey,
		BaseURL:         cfg.VippsBaseURL(),
		BrowserURL:      cfg.VippsBrowserURL(),
		HTTPClient:      &http.Client{Timeout: 15 * time.Second, Transport: otelhttp.NewTransport(http.DefaultTransport)},
	}
}

func (c *VippsClient) Enabled() bool {
	// Mock mode: enabled when base URL points to mock server
	if strings.Contains(c.BaseURL, "vipps-mock") || strings.Contains(c.BaseURL, "localhost:8090") {
		return true
	}
	return c.ClientID != "" && c.ClientSecret != ""
}

func (c *VippsClient) setSystemHeaders(req *http.Request) {
	if c.MSN != "" {
		req.Header.Set("Merchant-Serial-Number", c.MSN)
	}
	if c.SubscriptionKey != "" {
		req.Header.Set("Ocp-Apim-Subscription-Key", c.SubscriptionKey)
	}
	req.Header.Set("Vipps-System-Name", "brygge")
	req.Header.Set("Vipps-System-Version", "1.0.0")
}

func (c *VippsClient) AuthorizationURL(state string) string {
	params := url.Values{
		"client_id":     {c.ClientID},
		"response_type": {"code"},
		"scope":         {vippsScopes},
		"state":         {state},
		"redirect_uri":  {c.CallbackURL},
	}
	return c.BrowserURL + vippsAuthPath + "?" + params.Encode()
}

func (c *VippsClient) ExchangeCode(ctx context.Context, code string) (*VippsTokenResponse, error) {
	data := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {c.CallbackURL},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+vippsTokenPath, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(c.ClientID, c.ClientSecret)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("exchanging code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vipps token endpoint returned status %d", resp.StatusCode)
	}

	var tokenResp VippsTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}
	return &tokenResp, nil
}

func (c *VippsClient) GetUserInfo(ctx context.Context, accessToken string) (*VippsUserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+vippsUserInfoPath, nil)
	if err != nil {
		return nil, fmt.Errorf("creating userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	c.setSystemHeaders(req)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching userinfo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vipps userinfo endpoint returned status %d", resp.StatusCode)
	}

	var info VippsUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding userinfo: %w", err)
	}
	return &info, nil
}
