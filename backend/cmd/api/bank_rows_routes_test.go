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

// Every Tildel write under /accounting/bank-rows (assigns, dismiss, undo,
// pair-refund, pair-transfer) books or unbooks money, so it takes the
// treasurer/admin role and a fresh step-up: a board session reads the queue
// but every non-GET route answers 403. Walking the real router keeps a new
// bank-row write from slipping in without the gate.
func TestBankRowWritesNeedMoneyRoleAndFreshTOTP(t *testing.T) {
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
		sid, err := sessions.CreateSession(ctx, userID, clubID, "198.51.100.8:5555", "test-agent")
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

	const prefix = "/api/v1/admin/accounting/bank-rows/"
	var writes []string
	if err := chi.Walk(r, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(pattern, prefix) && method != http.MethodGet {
			writes = append(writes, method+" "+pattern)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(writes) < 9 {
		t.Fatalf("walked %d bank-row writes (%v); the prefix or the router changed", len(writes), writes)
	}
	sawPairTransfer := false
	for _, w := range writes {
		method, pattern, _ := strings.Cut(w, " ")
		path := concretePath(pattern)
		if rec := serveAs(r, method, path, board); rec.Code != http.StatusForbidden {
			t.Errorf("%s as board: %d, want 403", w, rec.Code)
		}
		if rec := serveAs(r, method, path, treasurerStale); rec.Code != http.StatusForbidden ||
			!strings.Contains(rec.Body.String(), "totp_fresh_required") {
			t.Errorf("%s as a treasurer without a fresh step-up: %d %s, want 403 totp_fresh_required", w, rec.Code, rec.Body)
		}
		if pattern == prefix+"pair-transfer" {
			sawPairTransfer = true
			// Past the gates the inert handler refuses the empty body.
			if rec := serveAs(r, method, path, treasurerFresh); rec.Code != http.StatusBadRequest {
				t.Errorf("%s as a fresh treasurer: %d %s, want it past the gates (400)", w, rec.Code, rec.Body)
			}
		}
	}
	if !sawPairTransfer {
		t.Errorf("POST %spair-transfer is not mounted (walked %v)", prefix, writes)
	}
}
