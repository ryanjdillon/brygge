package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// Without the refund service (accounting off, where the refund routes do not
// exist) the lists never mark a payment refundable, so no button leads to a
// dialog that cannot load.
func TestRefundFlagsAbsentWithoutRefundService(t *testing.T) {
	e := newRefundHTTPEnv(t)
	orderID, _ := e.paid(t, "9")
	invID := seedPayableInvoice(t, e.pool, e.clubID, e.userID, "open", 804)
	addOrderSource(t, e.pool, orderID, invID)
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE invoices SET online_paid_at = now(), online_paid_provider = 'stripe' WHERE id = $1`, invID); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{ClubSlug: e.h.cfg.ClubSlug}
	harbor := NewHarborHandler(e.pool, cfg, zerolog.Nop())
	invoices := NewInvoiceHandler(e.pool, cfg, nil, nil, nil, nil, zerolog.Nop())

	get := func(h http.HandlerFunc, url string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: e.userID, ClubID: e.clubID, Roles: []string{"treasurer"}}))
		rec := httptest.NewRecorder()
		h(rec, req)
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", url, rec.Code, rec.Body)
		}
		return out
	}
	listed := get(harbor.HandleRecentPayments, "/recent?kind=")["payments"].([]any)
	if len(listed) == 0 {
		t.Fatal("no payments listed")
	}
	for _, p := range listed {
		if p.(map[string]any)["can_refund"] != false {
			t.Errorf("payment %v is marked refundable", p)
		}
	}
	items := get(invoices.HandleListInvoices, "/invoices?status=sent")["items"].([]any)
	if len(items) == 0 {
		t.Fatal("no fakturas listed")
	}
	for _, it := range items {
		if it.(map[string]any)["refund_order_id"] != nil {
			t.Errorf("faktura %v carries a refund order", it)
		}
	}
}
