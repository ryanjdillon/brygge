// Package mail contains the Stalwart admin client and the role-gated
// inbox reconciler (DIL-275/276 phase 1).
//
// AdminClient targets Stalwart 0.15's management REST surface at
// http://127.0.0.1:8088/api. The exact ACL endpoints (assumed
// `/api/principal/:name/acl`) are not officially documented; the
// reconciler treats GET/SET errors as soft failures so an upstream
// schema change degrades to "skip this cycle" rather than crashing
// the backend. A JMAP `setACL` fallback path can replace these
// methods later without changing callers.
package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// AdminClient is a thin HTTP wrapper around Stalwart's admin REST API.
// Authentication is HTTP Basic — Stalwart 0.15 does not expose a
// separate bearer surface for the management endpoints we need here.
// We accept either:
//   - User + Password, or
//   - A pre-encoded "Basic <base64>" Token (compatibility with the
//     STALWART_ADMIN_TOKEN naming in the DIL-276 spec).
type AdminClient struct {
	baseURL string
	user    string
	pass    string
	token   string // optional pre-encoded "Basic …"
	http    *http.Client
	log     zerolog.Logger
}

// NewAdminClient constructs a client. `baseURL` should be the
// scheme+host of the Stalwart admin API (e.g.
// `http://127.0.0.1:8088`). If `token` is non-empty it overrides
// `user`/`pass`.
func NewAdminClient(baseURL, user, pass, token string, log zerolog.Logger) *AdminClient {
	return &AdminClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		user:    user,
		pass:    pass,
		token:   token,
		http:    &http.Client{Timeout: 10 * time.Second},
		log:     log.With().Str("component", "stalwart-admin").Logger(),
	}
}

func (c *AdminClient) auth(req *http.Request) {
	if c.token != "" {
		// Accept either a bare base64 blob or a full "Basic <…>" value.
		v := c.token
		if !strings.HasPrefix(strings.ToLower(v), "basic ") &&
			!strings.HasPrefix(strings.ToLower(v), "bearer ") {
			v = "Basic " + v
		}
		req.Header.Set("Authorization", v)
		return
	}
	req.SetBasicAuth(c.user, c.pass)
}

func principalName(address string) string {
	if i := strings.IndexByte(address, '@'); i >= 0 {
		return address[:i]
	}
	return address
}

// LookupPrincipal returns the Stalwart account id for a given email
// address. Returns "" with no error when the principal does not exist.
func (c *AdminClient) LookupPrincipal(ctx context.Context, email string) (string, error) {
	name := principalName(email)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/principal/"+name, nil)
	if err != nil {
		return "", err
	}
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("stalwart lookup %s: %s: %s", name, resp.Status, truncate(body, 200))
	}

	// Stalwart returns 200 with {"data": null} when a principal is
	// absent; treat that as "not found" too.
	var env struct {
		Data *struct {
			ID   any    `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return "", fmt.Errorf("stalwart lookup %s: decode: %w", name, err)
	}
	if env.Data == nil || env.Data.ID == nil {
		return "", nil
	}
	return fmt.Sprint(env.Data.ID), nil
}

// MintJMAPToken asks Stalwart for a short-TTL JMAP bearer token bound
// to the given principal. Phase 2 (read-only UI) consumes this; phase
// 1 only needs the surface in place so the client interface is stable.
func (c *AdminClient) MintJMAPToken(ctx context.Context, principalID string, ttl time.Duration) (string, error) {
	if principalID == "" {
		return "", errors.New("MintJMAPToken: empty principal id")
	}
	payload, err := json.Marshal(struct {
		Account string `json:"account"`
		TTLSecs int    `json:"ttl_seconds"`
	}{Account: principalID, TTLSecs: int(ttl.Seconds())})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/oauth/token", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	c.auth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("stalwart mint token %s: %s: %s", principalID, resp.Status, truncate(body, 200))
	}
	var env struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return "", fmt.Errorf("stalwart mint token: decode: %w", err)
	}
	return env.Data.AccessToken, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
