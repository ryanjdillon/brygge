package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

func payLinkReq(invoiceID, clubID string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/financials/invoices/"+invoiceID+"/pay-link", nil)
	ctx := middleware.WithClaims(r.Context(), &auth.Claims{UserID: "u", ClubID: clubID, Roles: []string{"treasurer"}})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("invoiceID", invoiceID)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	return r.WithContext(ctx)
}

func seedPayableInvoice(t *testing.T, db *pgxpool.Pool, clubID, userID, status string, num int) string {
	t.Helper()
	ctx := context.Background()
	var periodID string
	if err := db.QueryRow(ctx,
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 VALUES ($1, 2026, '2026-01-01', '2026-12-31', 'open') RETURNING id`, clubID,
	).Scan(&periodID); err != nil {
		// A club may already have a 2026 period from an earlier seed in the test.
		if err := db.QueryRow(ctx,
			`SELECT id FROM fiscal_periods WHERE club_id = $1 AND year = 2026`, clubID,
		).Scan(&periodID); err != nil {
			t.Fatalf("fiscal period: %v", err)
		}
	}
	var id string
	if err := db.QueryRow(ctx,
		`INSERT INTO invoices
		   (club_id, user_id, invoice_number, kid_number, due_date, total_amount,
		    status, fiscal_period_id, recipient_kind, recipient_email, sent_at)
		 VALUES ($1, $2, $3, $4, '2026-12-31', 450.00, $5, $6, 'private', 'm@example.com', now())
		 RETURNING id`,
		clubID, userID, num, fmt.Sprintf("%011d", num), status, periodID,
	).Scan(&id); err != nil {
		t.Fatalf("insert invoice: %v", err)
	}
	return id
}

func TestInvoicePayLink(t *testing.T) {
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

	invID := seedPayableInvoice(t, pool, clubID, userID, "open", 900)

	// Happy path: returns a /betal link.
	rec := httptest.NewRecorder()
	h.HandleGetInvoicePayLink(rec, payLinkReq(invID, clubID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		PayURL string `json:"pay_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(resp.PayURL, "https://example.org/betal/") {
		t.Errorf("pay_url = %q, want https://example.org/betal/<token>", resp.PayURL)
	}

	// The durable order + source were created and bound to the invoice.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_sources os JOIN orders o ON o.id = os.order_id
		  WHERE os.invoice_id = $1 AND o.club_id = $2 AND o.user_id = $3`,
		invID, clubID, userID,
	).Scan(&n); err != nil || n != 1 {
		t.Errorf("expected 1 bound order_source (user %s), got %d (err %v)", userID, n, err)
	}

	// Idempotent: a second call reuses the same token.
	rec2 := httptest.NewRecorder()
	h.HandleGetInvoicePayLink(rec2, payLinkReq(invID, clubID))
	var resp2 struct {
		PayURL string `json:"pay_url"`
	}
	_ = json.Unmarshal(rec2.Body.Bytes(), &resp2)
	if resp2.PayURL != resp.PayURL {
		t.Errorf("token not stable across calls: %q vs %q", resp.PayURL, resp2.PayURL)
	}

	// Voided invoice → 409.
	voidID := seedPayableInvoice(t, pool, clubID, userID, "voided", 901)
	recV := httptest.NewRecorder()
	h.HandleGetInvoicePayLink(recV, payLinkReq(voidID, clubID))
	if recV.Code != http.StatusConflict {
		t.Errorf("voided invoice: status = %d, want 409", recV.Code)
	}

	// Vipps not configured → 409.
	hNoVipps := &InvoiceHandler{db: pool, config: h.config, log: zerolog.Nop()}
	recN := httptest.NewRecorder()
	hNoVipps.HandleGetInvoicePayLink(recN, payLinkReq(invID, clubID))
	if recN.Code != http.StatusConflict {
		t.Errorf("no vipps: status = %d, want 409", recN.Code)
	}

	// Unknown invoice → 404.
	recNF := httptest.NewRecorder()
	h.HandleGetInvoicePayLink(recNF, payLinkReq("00000000-0000-0000-0000-000000000000", clubID))
	if recNF.Code != http.StatusNotFound {
		t.Errorf("unknown invoice: status = %d, want 404", recNF.Code)
	}
}
