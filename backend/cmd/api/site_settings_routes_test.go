package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// The site settings write, which carries the municipality codes that decide
// who counts as local (BRY-643), takes the treasurer or an admin with a fresh
// step-up; the board reads the settings and the municipality names.
func TestSiteSettingsRouteGates(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	sessions := auth.NewSessionService(pool)

	// session makes a user with a second factor and a session stepped up
	// minutesAgo minutes ago: inside the 12-hour admin window, and inside the
	// 10-minute fresh window only when minutesAgo is under 10.
	session := func(role string, minutesAgo int) string {
		t.Helper()
		userID, _ := testutil.SeedUser(t, pool, clubID, []string{role})
		if _, err := pool.Exec(ctx,
			`UPDATE users SET totp_secret_encrypted = '\x01', totp_enabled = true WHERE id = $1`, userID); err != nil {
			t.Fatal(err)
		}
		sid, err := sessions.CreateSession(ctx, userID, clubID, "198.51.100.7:5555", "test-agent")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE sessions SET totp_verified_at = NOW() - make_interval(mins => $2), mfa_method = 'totp' WHERE id = $1`,
			sid, minutesAgo); err != nil {
			t.Fatal(err)
		}
		return sid
	}
	r := inertRouter(t, sessions, nil)

	member := session("member", 1)
	board := session("board", 1)
	treasurerFresh := session("treasurer", 1)
	treasurerStale := session("treasurer", 30)
	adminFresh := session("admin", 1)

	const (
		site   = "/api/v1/admin/settings/site"
		lookup = "/api/v1/admin/settings/municipalities/9001"
	)
	// Past the gates the inert handlers answer 400 (no body), 200 (the
	// lookup without a register) or 500 (a nil pool).
	reached := func(code int) bool {
		switch code {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed:
			return false
		}
		return true
	}
	cases := []struct {
		name, method, path, sid string
		wantReached             bool
		wantBody                string
	}{
		{"member reads", http.MethodGet, site, member, false, ""},
		{"board reads", http.MethodGet, site, board, true, ""},
		{"board writes", http.MethodPatch, site, board, false, ""},
		{"stale treasurer writes", http.MethodPatch, site, treasurerStale, false, "totp_fresh_required"},
		{"fresh treasurer writes", http.MethodPatch, site, treasurerFresh, true, ""},
		{"fresh admin writes", http.MethodPatch, site, adminFresh, true, ""},
		{"member names a code", http.MethodGet, lookup, member, false, ""},
		{"board names a code", http.MethodGet, lookup, board, true, ""},
		{"no session names a code", http.MethodGet, lookup, "", false, ""},
	}
	for _, c := range cases {
		rec := serveAs(r, c.method, c.path, c.sid)
		if reached(rec.Code) != c.wantReached {
			t.Errorf("%s: %d %s, want reached=%v", c.name, rec.Code, rec.Body, c.wantReached)
		}
		if c.wantBody != "" && !strings.Contains(rec.Body.String(), c.wantBody) {
			t.Errorf("%s: body %s, want %q", c.name, rec.Body, c.wantBody)
		}
	}
}
