package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// A Vipps message rule picks the revenue account the payout reconcile books
// an unmatched charge to (BRY-676): its writes need the treasurer/admin role
// and a fresh TOTP, refuse a wildcard or too-short pattern and an account the
// club does not have active, are audited without the pattern, and a delete
// touches only the caller's club.
func TestMessageRuleRoutesGatedValidatedAndAudited(t *testing.T) {
	env := newAliasTestEnv(t, func(r chi.Router, svc *accounting.Service, as *audit.Service) {
		h := NewAccountingHandler(svc, as, nil, nil, nil, zerolog.Nop())
		r.Route("/gated", func(r chi.Router) {
			h.MountMessageRuleRoutes(r, middleware.RequireRole("treasurer", "admin"), middleware.RequireFreshTOTPDefault())
		})
		// Past the gate, as a stepped-up treasurer reaches the handlers.
		r.Route("/open", func(r chi.Router) { h.MountMessageRuleRoutes(r) })
	})
	ctx := context.Background()
	pool := env.club.Pool
	var code, inactive string
	if err := pool.QueryRow(ctx,
		`SELECT code FROM accounts WHERE club_id = $1 AND is_active ORDER BY code DESC LIMIT 1`, env.club.ID).Scan(&code); err != nil {
		t.Fatalf("active account: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`UPDATE accounts SET is_active = false
		  WHERE id = (SELECT id FROM accounts WHERE club_id = $1 AND is_active AND code <> $2 ORDER BY code LIMIT 1)
		  RETURNING code`, env.club.ID, code).Scan(&inactive); err != nil {
		t.Fatalf("deactivate account: %v", err)
	}
	const pattern = "Synthetic dugnad"
	body := map[string]any{"pattern": pattern, "account_code": code, "priority": 5}

	if rec := env.serve([]string{"board"}, http.MethodPost, "/gated/", body); rec.Code != http.StatusForbidden {
		t.Errorf("POST as board: status %d, want 403", rec.Code)
	}
	if rec := env.serve([]string{"board"}, http.MethodDelete, "/gated/00000000-0000-0000-0000-000000000001", nil); rec.Code != http.StatusForbidden {
		t.Errorf("DELETE as board: status %d, want 403", rec.Code)
	}
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/gated/", body); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), "totp_fresh_required") {
		t.Errorf("POST as treasurer without step-up: %d %s, want 403 totp_fresh_required", rec.Code, rec.Body)
	}
	if rec := env.serve([]string{"board"}, http.MethodGet, "/gated/", nil); rec.Code != http.StatusOK {
		t.Errorf("GET as board: status %d, want 200", rec.Code)
	}

	for _, tc := range []struct {
		name    string
		pattern string
		account string
	}{
		{"too short", "ab", code},
		{"too short once trimmed", "  ab  ", code},
		{"percent wildcard", "dug%nad", code},
		{"underscore wildcard", "dug_nad", code},
		{"backslash escape", `dug\nad`, code},
		{"unknown account", pattern, "99999"},
		{"inactive account", pattern, inactive},
	} {
		rec := env.serve([]string{"treasurer"}, http.MethodPost, "/open/",
			map[string]any{"pattern": tc.pattern, "account_code": tc.account})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("POST %s: %d %s, want 400", tc.name, rec.Code, rec.Body)
		}
	}
	if n := len(env.audit(audit.ActionVippsMessageRuleSaved)); n != 0 {
		t.Fatalf("refused rules audited %d times", n)
	}

	rec := env.serve([]string{"treasurer"}, http.MethodPost, "/open/", body)
	var rule accounting.VippsMessageRule
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &rule) != nil || rule.AccountCode != code || rule.Pattern != pattern {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body)
	}
	saved := env.audit(audit.ActionVippsMessageRuleSaved)
	if len(saved) != 1 || len(saved[0]) != 2 || saved[0]["account_code"] != code || saved[0]["priority"] != float64(5) {
		t.Errorf("saved audit = %v, want only account_code and priority", saved)
	}

	// Another club's rule is neither found nor deleted by this club.
	other := fin.NewClub(t, pool)
	var otherRule string
	if err := pool.QueryRow(ctx,
		`INSERT INTO vipps_message_rules (club_id, pattern, account_code, priority)
		 VALUES ($1, 'other club', '3900', 0) RETURNING id`, other.ID).Scan(&otherRule); err != nil {
		t.Fatalf("other club rule: %v", err)
	}
	for _, id := range []string{otherRule, "00000000-0000-0000-0000-000000000001", "not-a-uuid"} {
		if rec := env.serve([]string{"treasurer"}, http.MethodDelete, "/open/"+id, nil); rec.Code != http.StatusNoContent {
			t.Errorf("DELETE %s: %d %s, want 204", id, rec.Code, rec.Body)
		}
	}
	var otherLeft bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM vipps_message_rules WHERE id = $1)`, otherRule).Scan(&otherLeft); err != nil || !otherLeft {
		t.Errorf("another club's rule was deleted (err %v)", err)
	}
	if n := len(env.audit(audit.ActionVippsMessageRuleRemoved)); n != 0 {
		t.Errorf("deleting no rule of the club audited %d times", n)
	}

	if rec := env.serve([]string{"treasurer"}, http.MethodDelete, "/open/"+rule.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body)
	}
	removed := env.audit(audit.ActionVippsMessageRuleRemoved)
	if len(removed) != 1 || len(removed[0]) != 2 || removed[0]["account_code"] != code || removed[0]["priority"] != float64(5) {
		t.Errorf("removed audit = %v, want the removed rule's account_code and priority", removed)
	}
}
