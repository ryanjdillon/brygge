package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// A client that disconnects while the batch runs does not stop it half way,
// and the batch's audit row is written for the charges already committed.
// The request context here is cancelled before the handler starts, which is
// the strictest form of the disconnect.
func TestApplyVippsFallbackReclassSurvivesClientDisconnect(t *testing.T) {
	var svc *accounting.Service
	env := newAliasTestEnv(t, func(r chi.Router, s *accounting.Service, as *audit.Service) {
		svc = s
		h := NewAccountingHandler(s, as, nil, nil, nil, zerolog.Nop())
		h.MountLedgerRoutes(r)
	})
	ctx := context.Background()
	pool := env.club.Pool
	if _, err := pool.Exec(ctx, `UPDATE clubs SET vipps_auto_assign = true WHERE id = $1`, env.club.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO price_items (club_id, category, name, amount, unit, is_active, audience)
		 VALUES ($1, 'guest', 'Gjesteplass synthetic', 800, 'day', true, 'all')`, env.club.ID); err != nil {
		t.Fatal(err)
	}
	row := env.club.NewVippsRow(t, fin.VippsRowOpts{Cents: 80000, CustomerName: "Walk Up"})
	if _, err := svc.CreateVippsChargeBilag(ctx, env.club.ID, row, "3900", "", env.club.Treasurer.ID); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.PreviewVippsFallbackReclass(ctx, env.club.ID, "")
	if err != nil || len(preview.Items) != 1 {
		t.Fatalf("preview = %+v, %v", preview, err)
	}

	body, _ := json.Marshal(map[string]any{"targets": map[string]string{preview.Items[0].EntryID: "3200"}})
	reqCtx, cancel := context.WithCancel(ctx)
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/sync/vipps-fallback-reclass", bytes.NewReader(body)).WithContext(reqCtx)
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{
		UserID: env.club.Treasurer.ID, ClubID: env.club.ID, Roles: []string{"treasurer"},
	}))
	rec := httptest.NewRecorder()
	env.r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("apply on a cancelled request: %d %s", rec.Code, rec.Body)
	}
	got := env.audit(audit.ActionAccountingVippsFallbackReclassified)
	if len(got) != 1 || got[0]["reclassified"] != float64(1) {
		t.Errorf("audit rows = %v, want the batch recorded despite the disconnect", got)
	}
}
