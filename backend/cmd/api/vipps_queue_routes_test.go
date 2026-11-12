package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// The Vipps queue's settings decide where money lands: a payer alias picks
// the member's faktura a payment settles (BRY-383) and a message rule picks
// the revenue account the payout confirm books a charge to (BRY-676). So
// every write under /accounting/vipps/customer-aliases and
// /accounting/vipps/message-rules takes the treasurer/admin role and a fresh
// step-up: a board session answers 403, a treasurer with a stale step-up
// 403 totp_fresh_required. Walking the real router keeps a new write there
// from slipping in without the gate.
func TestVippsQueueSettingWritesNeedMoneyRoleAndFreshTOTP(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	sessions := auth.NewSessionService(pool)

	session := func(role string, minutesAgo int) string {
		t.Helper()
		userID, _ := testutil.SeedUser(t, pool, clubID, []string{role})
		if _, err := pool.Exec(ctx,
			`UPDATE users SET totp_secret_encrypted = '\x01', totp_enabled = true WHERE id = $1`, userID); err != nil {
			t.Fatal(err)
		}
		sid, err := sessions.CreateSession(ctx, userID, clubID, "198.51.100.9:5555", "test-agent")
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
	board := session("board", 1)
	treasurerStale := session("treasurer", 30)
	treasurerFresh := session("treasurer", 1)

	prefixes := []string{
		"/api/v1/admin/accounting/vipps/customer-aliases/",
		"/api/v1/admin/accounting/vipps/message-rules/",
	}
	var writes []string
	if err := chi.Walk(r, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == http.MethodGet {
			return nil
		}
		for _, p := range prefixes {
			if strings.HasPrefix(pattern, p) {
				writes = append(writes, method+" "+pattern)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	want := map[string]bool{
		"POST " + prefixes[0]:                 false,
		"DELETE " + prefixes[0] + "{aliasID}": false,
		"POST " + prefixes[1]:                 false,
		"DELETE " + prefixes[1] + "{ruleID}":  false,
	}
	for _, w := range writes {
		if _, ok := want[w]; ok {
			want[w] = true
		}
		method, pattern, _ := strings.Cut(w, " ")
		path := concretePath(pattern)
		if rec := serveAs(r, method, path, board); rec.Code != http.StatusForbidden {
			t.Errorf("%s as board: %d, want 403", w, rec.Code)
		}
		if rec := serveAs(r, method, path, treasurerStale); rec.Code != http.StatusForbidden ||
			!strings.Contains(rec.Body.String(), "totp_fresh_required") {
			t.Errorf("%s as a treasurer without a fresh step-up: %d %s, want 403 totp_fresh_required", w, rec.Code, rec.Body)
		}
		if method == http.MethodPost {
			// Past the gates the inert handler refuses the empty body.
			if rec := serveAs(r, method, path, treasurerFresh); rec.Code != http.StatusBadRequest {
				t.Errorf("%s as a fresh treasurer: %d %s, want it past the gates (400)", w, rec.Code, rec.Body)
			}
		}
	}
	for w, seen := range want {
		if !seen {
			t.Errorf("%s is not mounted (walked %v)", w, writes)
		}
	}
}
