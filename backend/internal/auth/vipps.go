package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/brygge-klubb/brygge/internal/config"
)

const (
	vippsTestBaseURL = "https://apitest.vipps.no"
	vippsProdBaseURL = "https://api.vipps.no"

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
	ClientID     string
	ClientSecret string
	CallbackURL  string
	TestMode     bool
	HTTPClient   *http.Client
}

func NewVippsClient(cfg *config.Config) *VippsClient {
	return &VippsClient{
		ClientID:     cfg.VippsClientID,
		ClientSecret: cfg.VippsClientSecret,
		CallbackURL:  cfg.VippsCallbackURL,
		TestMode:     cfg.VippsTestMode,
		HTTPClient:   http.DefaultClient,
	}
}

func (c *VippsClient) baseURL() string {
	if c.TestMode {
		return vippsTestBaseURL
	}
	return vippsProdBaseURL
}

func (c *VippsClient) AuthorizationURL(state string) string {
	params := url.Values{
		"client_id":     {c.ClientID},
		"response_type": {"code"},
		"scope":         {vippsScopes},
		"state":         {state},
		"redirect_uri":  {c.CallbackURL},
	}
	return c.baseURL() + vippsAuthPath + "?" + params.Encode()
}

func (c *VippsClient) ExchangeCode(ctx context.Context, code string) (*VippsTokenResponse, error) {
	data := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {c.CallbackURL},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+vippsTokenPath, strings.NewReader(data.Encode()))
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL()+vippsUserInfoPath, nil)
	if err != nil {
		return nil, fmt.Errorf("creating userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

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
