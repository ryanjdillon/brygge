package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

// TestVippsAmountThreshold covers BRY-283: a per-club cap suppresses the Vipps
// link at issuance and refuses it at mint, for both under- and over-limit.
func TestVippsAmountThreshold(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})

	h := &InvoiceHandler{
		db:     pool,
		config: &config.Config{FrontendURL: "https://example.org"},
		vipps:  vipps.New("", "", "", "", "", zerolog.Nop()),
		log:    zerolog.Nop(),
	}

	// seedPayableInvoice issues invoices of 450.00 kr.
	invID := seedPayableInvoice(t, pool, clubID, userID, "open", 950)

	setCap := func(v any) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE clubs SET vipps_max_amount = $1 WHERE id = $2`, v, clubID); err != nil {
			t.Fatalf("set cap: %v", err)
		}
	}

	// No cap → Vipps allowed, token minted, pay-link returned.
	setCap(nil)
	if !h.invoiceVippsAllowed(ctx, clubID, invID) {
		t.Fatal("no cap: expected Vipps allowed")
	}
	token := h.ensureFakturaOrderToken(ctx, clubID, invID, true)
	if token == "" {
		t.Fatal("no cap: expected a token")
	}
	var orderID string
	if err := pool.QueryRow(ctx, `SELECT id FROM orders WHERE token = $1`, token).Scan(&orderID); err != nil {
		t.Fatalf("load order: %v", err)
	}

	// Cap above the amount (1000 > 450) → still allowed.
	setCap(1000)
	if !h.invoiceVippsAllowed(ctx, clubID, invID) {
		t.Error("cap 1000: expected Vipps allowed for a 450 invoice")
	}

	// Cap below the amount (100 < 450) → suppressed everywhere.
	setCap(100)
	if h.invoiceVippsAllowed(ctx, clubID, invID) {
		t.Error("cap 100: expected Vipps suppressed for a 450 invoice")
	}
	if tok := h.ensureFakturaOrderToken(ctx, clubID, invID, true); tok != "" {
		t.Errorf("cap 100: issuance should return no token, got %q", tok)
	}
	// Redeem gate: the order minted while uncapped must now refuse at mint time,
	// before any Vipps call — closing the already-issued-link leak.
	if _, err := h.mintOrderPayment(ctx, orderID); err != errVippsOverLimit {
		t.Errorf("cap 100: mint should refuse with errVippsOverLimit, got %v", err)
	}

	// Admin pay-link endpoint reports the distinct over-limit reason.
	rec := httptest.NewRecorder()
	h.HandleGetInvoicePayLink(rec, payLinkReq(invID, clubID))
	if rec.Code != http.StatusConflict {
		t.Fatalf("cap 100: pay-link status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "vipps_over_limit") {
		t.Errorf("cap 100: pay-link body = %q, want vipps_over_limit", body)
	}
}
