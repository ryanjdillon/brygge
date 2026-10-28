package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/payments"
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
		db:        pool,
		config:    &config.Config{FrontendURL: "https://example.org"},
		providers: payments.NewRegistry(vipps.NewPaymentProvider(vipps.New("", "", "", "", "", zerolog.Nop()))),
		log:       zerolog.Nop(),
	}

	// seedPayableInvoice issues invoices of 450.00 kr.
	invID := seedPayableInvoice(t, pool, clubID, userID, "open", 950)

	setCap := func(v any) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE clubs SET online_max_amount = $1 WHERE id = $2`, v, clubID); err != nil {
			t.Fatalf("set cap: %v", err)
		}
	}

	// No cap → Vipps allowed, token minted, pay-link returned.
	setCap(nil)
	if !h.invoiceOnlinePaymentAllowed(ctx, clubID, invID) {
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
	if !h.invoiceOnlinePaymentAllowed(ctx, clubID, invID) {
		t.Error("cap 1000: expected Vipps allowed for a 450 invoice")
	}

	// Cap below the amount (100 < 450), Auto override → suppressed everywhere.
	setCap(100)
	setOptIn := func(v any) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE invoices SET online_payment_opt_in = $1 WHERE id = $2`, v, invID); err != nil {
			t.Fatalf("set opt_in: %v", err)
		}
	}
	setOptIn(nil) // Auto
	if h.invoiceOnlinePaymentAllowed(ctx, clubID, invID) {
		t.Error("cap 100, Auto: expected Vipps suppressed for a 450 invoice")
	}
	if tok := h.ensureFakturaOrderToken(ctx, clubID, invID, true); tok != "" {
		t.Errorf("cap 100, Auto: issuance should return no token, got %q", tok)
	}
	// Redeem gate: the order minted while uncapped must now refuse at mint time,
	// before any Vipps call — closing the already-issued-link leak.
	if _, err := h.mintOrderPayment(ctx, vipps.ProviderName, orderID, ""); err != errOnlinePaymentBlocked {
		t.Errorf("cap 100, Auto: mint should refuse with errOnlinePaymentBlocked, got %v", err)
	}
	// Admin pay-link endpoint reports the distinct over-limit reason.
	rec := httptest.NewRecorder()
	h.HandleGetInvoicePayLink(rec, payLinkReq(invID, clubID))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "vipps_over_limit") {
		t.Errorf("cap 100, Auto: pay-link = %d %q, want 409 vipps_over_limit", rec.Code, rec.Body.String())
	}

	// Override On → forces Vipps on even above the cap: issuance mints, mint allows.
	setOptIn(true)
	if !h.invoiceOnlinePaymentAllowed(ctx, clubID, invID) {
		t.Error("On: expected Vipps allowed above cap")
	}
	if tok := h.ensureFakturaOrderToken(ctx, clubID, invID, true); tok == "" {
		t.Error("On: issuance should return a token above cap")
	}
	// Check the mint-time gate directly (not mintOrderPayment, which would call
	// the Vipps API): the order must no longer be blocked.
	if blocked, err := h.orderHasOnlineBlockedSource(ctx, orderID); err != nil || blocked {
		t.Errorf("On: order should not be blocked above cap (blocked=%v err=%v)", blocked, err)
	}

	// Override Off → forces Vipps off even when a generous cap would allow it.
	setCap(10000)
	setOptIn(false)
	if h.invoiceOnlinePaymentAllowed(ctx, clubID, invID) {
		t.Error("Off: expected Vipps suppressed below cap")
	}
	if tok := h.ensureFakturaOrderToken(ctx, clubID, invID, true); tok != "" {
		t.Errorf("Off: issuance should return no token, got %q", tok)
	}
}

// TestSetInvoiceVippsLink covers the per-invoice Auto/On/Off override endpoint.
func TestSetInvoiceVippsLink(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"treasurer"})
	h := &InvoiceHandler{db: pool, config: &config.Config{}, log: zerolog.Nop()}
	invID := seedPayableInvoice(t, pool, clubID, userID, "open", 960)

	req := func(invoiceID, body string) *http.Request {
		r := httptest.NewRequest(http.MethodPatch, "/x", strings.NewReader(body))
		c := middleware.WithClaims(r.Context(), &auth.Claims{UserID: "staff", ClubID: clubID, Roles: []string{"treasurer"}})
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("invoiceID", invoiceID)
		return r.WithContext(context.WithValue(c, chi.RouteCtxKey, rctx))
	}
	// Both the provider-neutral column and the legacy one are written until the
	// legacy one is dropped.
	stored := func() *bool {
		var v, legacy *bool
		if err := pool.QueryRow(ctx,
			`SELECT online_payment_opt_in, vipps_payment_link_opt_in FROM invoices WHERE id = $1`, invID,
		).Scan(&v, &legacy); err != nil {
			t.Fatalf("read opt_in: %v", err)
		}
		if (v == nil) != (legacy == nil) || (v != nil && *v != *legacy) {
			t.Errorf("online_payment_opt_in = %v but vipps_payment_link_opt_in = %v", v, legacy)
		}
		return v
	}

	cases := []struct {
		body string
		want *bool
	}{
		{`{"opt_in":true}`, ptr(true)},
		{`{"opt_in":false}`, ptr(false)},
		{`{"opt_in":null}`, nil},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		h.HandleSetInvoiceVippsLink(rec, req(invID, tc.body))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("body %s: status = %d, want 204 (%s)", tc.body, rec.Code, rec.Body.String())
		}
		got := stored()
		if (got == nil) != (tc.want == nil) || (got != nil && tc.want != nil && *got != *tc.want) {
			t.Errorf("body %s: stored = %v, want %v", tc.body, got, tc.want)
		}
	}

	// Cross-club invoice → 404.
	otherClub := testutil.SeedClub(t, pool)
	otherUser, _ := testutil.SeedUser(t, pool, otherClub, []string{"member"})
	otherInv := seedPayableInvoice(t, pool, otherClub, otherUser, "open", 961)
	rec := httptest.NewRecorder()
	h.HandleSetInvoiceVippsLink(rec, req(otherInv, `{"opt_in":true}`))
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-club: status = %d, want 404", rec.Code)
	}
}

func ptr(b bool) *bool { return &b }
