package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

func userPayLinkReq(userID, clubID string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/financials/users/"+userID+"/pay-link", nil)
	ctx := middleware.WithClaims(r.Context(), &auth.Claims{UserID: "staff", ClubID: clubID, Roles: []string{"treasurer"}})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("userID", userID)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	return r.WithContext(ctx)
}

func payablesReq(token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/betal/bruker/"+token, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", token)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestUserPayLink(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)

	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})

	h := &InvoiceHandler{
		db:     pool,
		config: &config.Config{FrontendURL: "https://example.org"},
		vipps:  vipps.New("", "", "", "", "", zerolog.Nop()),
		log:    zerolog.Nop(),
	}

	// Happy path: a durable /betal/bruker link.
	rec := httptest.NewRecorder()
	h.HandleGetUserPayLink(rec, userPayLinkReq(userID, clubID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		PayURL string `json:"pay_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(resp.PayURL, "https://example.org/betal/bruker/") {
		t.Errorf("pay_url = %q, want https://example.org/betal/bruker/<token>", resp.PayURL)
	}

	// Idempotent: the same token is reused across calls.
	rec2 := httptest.NewRecorder()
	h.HandleGetUserPayLink(rec2, userPayLinkReq(userID, clubID))
	var resp2 struct {
		PayURL string `json:"pay_url"`
	}
	_ = json.Unmarshal(rec2.Body.Bytes(), &resp2)
	if resp2.PayURL != resp.PayURL {
		t.Errorf("token not stable across calls: %q vs %q", resp.PayURL, resp2.PayURL)
	}

	// Vipps not configured → 409.
	hNoVipps := &InvoiceHandler{db: pool, config: h.config, log: zerolog.Nop()}
	recN := httptest.NewRecorder()
	hNoVipps.HandleGetUserPayLink(recN, userPayLinkReq(userID, clubID))
	if recN.Code != http.StatusConflict {
		t.Errorf("no vipps: status = %d, want 409", recN.Code)
	}

	// Unknown user → 404.
	recNF := httptest.NewRecorder()
	h.HandleGetUserPayLink(recNF, userPayLinkReq("00000000-0000-0000-0000-000000000000", clubID))
	if recNF.Code != http.StatusNotFound {
		t.Errorf("unknown user: status = %d, want 404", recNF.Code)
	}
}

func TestGetPayables(t *testing.T) {
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

	// Two payable invoices, plus one voided and one already received via Vipps —
	// both of which must be excluded from the payables list.
	open1 := seedPayableInvoice(t, pool, clubID, userID, "open", 910)
	open2 := seedPayableInvoice(t, pool, clubID, userID, "open", 911)
	_ = seedPayableInvoice(t, pool, clubID, userID, "voided", 912)
	received := seedPayableInvoice(t, pool, clubID, userID, "open", 913)
	if _, err := pool.Exec(ctx, `UPDATE invoices SET vipps_paid_at = now() WHERE id = $1`, received); err != nil {
		t.Fatalf("mark received: %v", err)
	}

	token := h.ensureUserPayablesToken(ctx, clubID, userID, true)
	if token == "" {
		t.Fatal("failed to mint payables token")
	}

	rec := httptest.NewRecorder()
	h.HandleGetPayables(rec, payablesReq(token))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ClubName   string `json:"club_name"`
		MemberName string `json:"member_name"`
		Invoices   []struct {
			ID       string `json:"id"`
			PayToken string `json:"pay_token"`
			Paid     bool   `json:"paid"`
		} `json:"invoices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Invoices) != 2 {
		t.Fatalf("got %d invoices, want 2 (open only); body=%s", len(resp.Invoices), rec.Body.String())
	}
	got := map[string]bool{resp.Invoices[0].ID: true, resp.Invoices[1].ID: true}
	if !got[open1] || !got[open2] {
		t.Errorf("payables did not contain both open invoices: %+v", resp.Invoices)
	}
	for _, inv := range resp.Invoices {
		if inv.PayToken == "" {
			t.Errorf("invoice %s missing pay_token", inv.ID)
		}
		if inv.Paid {
			t.Errorf("invoice %s should not be marked paid", inv.ID)
		}
	}

	// Over the club's Vipps cap (BRY-283): the invoices still list, but with no
	// pay_token, and the club bank account is surfaced for bank transfer.
	if _, err := pool.Exec(ctx, `UPDATE clubs SET vipps_max_amount = 100 WHERE id = $1`, clubID); err != nil {
		t.Fatalf("set cap: %v", err)
	}
	recCap := httptest.NewRecorder()
	h.HandleGetPayables(recCap, payablesReq(token))
	var capped struct {
		BankAccount string `json:"bank_account"`
		Invoices    []struct {
			PayToken string `json:"pay_token"`
		} `json:"invoices"`
	}
	_ = json.Unmarshal(recCap.Body.Bytes(), &capped)
	if len(capped.Invoices) != 2 {
		t.Fatalf("capped: got %d invoices, want 2", len(capped.Invoices))
	}
	for _, inv := range capped.Invoices {
		if inv.PayToken != "" {
			t.Errorf("capped: expected empty pay_token (bank transfer only), got %q", inv.PayToken)
		}
	}

	// Unknown token → 404.
	recNF := httptest.NewRecorder()
	h.HandleGetPayables(recNF, payablesReq("deadbeef"))
	if recNF.Code != http.StatusNotFound {
		t.Errorf("unknown token: status = %d, want 404", recNF.Code)
	}
}
