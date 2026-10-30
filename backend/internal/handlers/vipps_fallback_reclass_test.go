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

// The reclass of posted Vipps revenue on 3900 (BRY-557): the preview is
// readable by the board, the apply needs treasurer/admin and a fresh TOTP,
// writes one audit row per batch with counts, totals per account and the
// replaced bilag, and refuses while auto-assign is off.
func TestVippsFallbackReclassRoutesGatedAndAudited(t *testing.T) {
	var svc *accounting.Service
	env := newAliasTestEnv(t, func(r chi.Router, s *accounting.Service, as *audit.Service) {
		svc = s
		h := NewAccountingHandler(s, as, nil, nil, nil, zerolog.Nop())
		r.Route("/gated", func(r chi.Router) {
			h.MountLedgerRoutes(r, middleware.RequireRole("treasurer", "admin"), middleware.RequireFreshTOTPDefault())
		})
		r.Route("/open", func(r chi.Router) { h.MountLedgerRoutes(r) })
	})
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := env.club.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}
	exec(`UPDATE clubs SET vipps_auto_assign = true WHERE id = $1`, env.club.ID)
	exec(`INSERT INTO price_items (club_id, category, name, amount, unit, is_active, audience)
	      VALUES ($1, 'guest', 'Gjesteplass synthetic', 800, 'day', true, 'all')`, env.club.ID)
	row := env.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 80000, CustomerName: "Walk Up"})
	if _, err := svc.CreateVippsChargeBilag(ctx, env.club.ID, row, "3900", "", env.club.Treasurer.ID); err != nil {
		t.Fatalf("park charge: %v", err)
	}

	rec := env.serve([]string{"board"}, http.MethodPost, "/gated/sync/vipps-fallback-reclass/preview", map[string]string{})
	if rec.Code != http.StatusOK {
		t.Fatalf("preview as board: %d %s", rec.Code, rec.Body)
	}
	var preview accounting.VippsFallbackReclassResult
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil || preview.Proposed != 1 || len(preview.Items) != 1 {
		t.Fatalf("preview = %s (%v)", rec.Body, err)
	}
	entryID := preview.Items[0].EntryID
	body := map[string]any{"targets": map[string]string{entryID: "3200"}}

	if rec := env.serve([]string{"board"}, http.MethodPost, "/gated/sync/vipps-fallback-reclass", body); rec.Code != http.StatusForbidden {
		t.Errorf("apply as board: %d, want 403", rec.Code)
	}
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/gated/sync/vipps-fallback-reclass", body); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), "totp_fresh_required") {
		t.Errorf("apply as treasurer without step-up: %d %s, want 403 totp_fresh_required", rec.Code, rec.Body)
	}
	if got := env.audit(audit.ActionAccountingVippsFallbackReclassified); len(got) != 0 {
		t.Fatalf("a refused apply was audited: %v", got)
	}
	if rec := env.serve([]string{"treasurer"}, http.MethodPost, "/open/sync/vipps-fallback-reclass", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Errorf("apply without targets: %d, want 400", rec.Code)
	}

	// Switch off: 409 with a code, nothing booked, nothing audited.
	exec(`UPDATE clubs SET vipps_auto_assign = false WHERE id = $1`, env.club.ID)
	rec = env.serve([]string{"treasurer"}, http.MethodPost, "/open/sync/vipps-fallback-reclass", body)
	var refused map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &refused)
	if rec.Code != http.StatusConflict || refused["code"] != "auto_assign_off" {
		t.Errorf("apply with auto-assign off: %d %s, want 409 auto_assign_off", rec.Code, rec.Body)
	}
	if got := env.audit(audit.ActionAccountingVippsFallbackReclassified); len(got) != 0 {
		t.Fatalf("a refused apply was audited: %v", got)
	}
	exec(`UPDATE clubs SET vipps_auto_assign = true WHERE id = $1`, env.club.ID)

	rec = env.serve([]string{"treasurer"}, http.MethodPost, "/open/sync/vipps-fallback-reclass", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	var res accounting.VippsFallbackReclassResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || !res.Applied || res.Reclassified != 1 {
		t.Fatalf("result = %s (%v)", rec.Body, err)
	}
	got := env.audit(audit.ActionAccountingVippsFallbackReclassified)
	if len(got) != 1 {
		t.Fatalf("audit rows = %v, want one per batch", got)
	}
	a := got[0]
	totals, _ := a["totals"].([]any)
	replaced, _ := a["replaced"].([]any)
	if a["reclassified"] != float64(1) || a["requested"] != float64(1) || a["from_account"] != "3900" ||
		a["failed"] != float64(0) || len(totals) != 1 || len(replaced) != 1 {
		t.Errorf("audit = %v", a)
	}
	if first, _ := totals[0].(map[string]any); first["account_code"] != "3200" || first["count"] != float64(1) || first["amount"] != float64(800) {
		t.Errorf("audit totals = %v, want 3200: 1 charge, 800 kr", totals)
	}
	if rep, _ := replaced[0].(map[string]any); rep["to"] != "3200" || rep["reversal"] == nil || rep["replacement"] == nil {
		t.Errorf("audit replaced = %v", replaced)
	}

	// A second run does nothing, and is audited as a batch of zero.
	rec = env.serve([]string{"treasurer"}, http.MethodPost, "/open/sync/vipps-fallback-reclass", body)
	var again accounting.VippsFallbackReclassResult
	if err := json.Unmarshal(rec.Body.Bytes(), &again); err != nil || rec.Code != http.StatusOK || again.Reclassified != 0 || len(again.StaleIDs) != 1 {
		t.Errorf("second apply = %d %s", rec.Code, rec.Body)
	}
}
