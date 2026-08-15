package vipps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// vippsInt tolerates Vipps returning numeric fields as JSON strings — the
// Access Token API sends "expires_in": "3599" (quoted), not a bare number —
// while still accepting a plain JSON number.
type vippsInt int

func (v *vippsInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*v = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("vippsInt %q: %w", s, err)
	}
	*v = vippsInt(n)
	return nil
}

// Client is a reusable Vipps ePayment API client. It handles access-token
// fetch + caching so callers never need to manage credentials directly.
// Safe for concurrent use.
type Client struct {
	baseURL         string
	clientID        string
	clientSecret    string
	subscriptionKey string
	msn             string
	log             zerolog.Logger
	http            *http.Client

	mu          sync.Mutex
	tokenValue  string
	tokenExpiry time.Time
}

// New returns a Client wired to the given base URL and credentials.
// Use config.VippsBaseURL() for the baseURL so mock / test / prod is
// selected from environment automatically.
func New(baseURL, clientID, clientSecret, subscriptionKey, msn string, log zerolog.Logger) *Client {
	return &Client{
		baseURL:         baseURL,
		clientID:        clientID,
		clientSecret:    clientSecret,
		subscriptionKey: subscriptionKey,
		msn:             msn,
		log:             log.With().Str("component", "vipps").Logger(),
		http:            &http.Client{Timeout: 15 * time.Second},
	}
}

// MSN returns the Merchant Serial Number for this client.
func (c *Client) MSN() string { return c.msn }

// VerifyResult summarises a successful Access Token API handshake without
// exposing the bearer token itself. Returned by Verify for operator
// credential checks and structured logging.
type VerifyResult struct {
	BaseURL     string
	MSN         string
	TokenMasked string
	ExpiresAt   time.Time
}

// Verify performs an end-to-end Access Token API handshake with the
// configured credentials and returns a non-sensitive summary of the
// result. It bypasses the token cache so each call exercises the live
// credential path — intended for the cmd/vipps-verify operator check, not
// the hot path. A nil error means client_id/client_secret/subscription-key/
// MSN are all accepted by Vipps for this environment.
func (c *Client) Verify(ctx context.Context) (VerifyResult, error) {
	c.mu.Lock()
	c.tokenValue = ""
	c.tokenExpiry = time.Time{}
	c.mu.Unlock()

	token, err := c.accessToken(ctx)
	if err != nil {
		return VerifyResult{BaseURL: c.baseURL, MSN: c.msn}, err
	}

	c.mu.Lock()
	expiry := c.tokenExpiry
	c.mu.Unlock()

	return VerifyResult{
		BaseURL:     c.baseURL,
		MSN:         c.msn,
		TokenMasked: maskToken(token),
		ExpiresAt:   expiry,
	}, nil
}

// maskToken reduces a bearer token to a non-reversible fingerprint safe for
// logs: a short prefix plus the length. Never log the full token.
func maskToken(tok string) string {
	if len(tok) <= 6 {
		return fmt.Sprintf("***(len=%d)", len(tok))
	}
	return fmt.Sprintf("%s…(len=%d)", tok[:6], len(tok))
}

// accessToken returns a cached bearer token, refreshing it proactively
// when fewer than 10 minutes remain.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.tokenValue != "" && time.Until(c.tokenExpiry) > 10*time.Minute {
		return c.tokenValue, nil
	}

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/accesstoken/get", http.NoBody)
	if err != nil {
		return "", fmt.Errorf("vipps access token: %w", err)
	}
	req.Header.Set("client_id", c.clientID)
	req.Header.Set("client_secret", c.clientSecret)
	req.Header.Set("Ocp-Apim-Subscription-Key", c.subscriptionKey)
	req.Header.Set("Merchant-Serial-Number", c.msn)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("vipps access token: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	c.log.Debug().
		Str("url", req.URL.String()).
		Int("status", resp.StatusCode).
		Dur("latency_ms", time.Since(start)).
		Msg("vipps accesstoken/get")

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("vipps access token %d: %s", resp.StatusCode, truncate(raw, 200))
	}

	var body struct {
		AccessToken string   `json:"access_token"`
		ExpiresIn   vippsInt `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", fmt.Errorf("vipps access token decode: %w", err)
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("vipps access token: empty token in response")
	}

	ttl := time.Duration(body.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	c.tokenValue = body.AccessToken
	c.tokenExpiry = time.Now().Add(ttl)
	return c.tokenValue, nil
}

// do executes an authenticated Vipps API call. bodyIn is JSON-encoded as the
// request body (nil → no body). On non-2xx the response body is included in
// the error. If the server returns 401 the token cache is invalidated and the
// request is retried once.
func (c *Client) do(ctx context.Context, method, path string, idempotencyKey string, bodyIn any, out any) error {
	return c.doAttempt(ctx, method, path, idempotencyKey, bodyIn, out, false)
}

func (c *Client) doAttempt(ctx context.Context, method, path string, idempotencyKey string, bodyIn any, out any, isRetry bool) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}

	var reqBody io.Reader
	if bodyIn != nil {
		b, err := json.Marshal(bodyIn)
		if err != nil {
			return fmt.Errorf("vipps %s %s marshal: %w", method, path, err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("vipps %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Ocp-Apim-Subscription-Key", c.subscriptionKey)
	req.Header.Set("Merchant-Serial-Number", c.msn)
	req.Header.Set("Vipps-System-Name", "brygge")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if bodyIn != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("vipps %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	c.log.Debug().
		Str("method", method).
		Str("path", path).
		Int("status", resp.StatusCode).
		Str("idempotency_key", idempotencyKey).
		Dur("latency_ms", time.Since(start)).
		Msg("vipps api call")

	if resp.StatusCode == http.StatusUnauthorized && !isRetry {
		c.mu.Lock()
		c.tokenValue = ""
		c.mu.Unlock()
		return c.doAttempt(ctx, method, path, idempotencyKey, bodyIn, out, true)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("vipps %s %s → %d: %s", method, path, resp.StatusCode, truncate(raw, 300))
	}

	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("vipps %s %s decode: %w", method, path, err)
		}
	}
	return nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
