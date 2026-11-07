package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/membership"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

func (e *bankJoinEnv) joinMe(t *testing.T, userID string, userRoles ...string) joinMeResponse {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/join/me", e.inv.HandleJoinMe)
	req := httptest.NewRequest(http.MethodGet, "/join/me", nil)
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: userID, ClubID: e.clubID, Roles: userRoles}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
	return decodeJSON[joinMeResponse](t, rec)
}

// GET /join/me follows a join from confirm to membership without changing
// it: the pending order's pay link, then the bank faktura with its KID, then
// member once the faktura is paid.
func TestJoinMeStates(t *testing.T) {
	e := setupBankJoinEnv(t)
	ctx := context.Background()
	userID, orderToken := e.applicant(t, "me@example.com")
	wantURL := checkoutURL(e.cfg, orderToken)

	got := e.joinMe(t, userID, "applicant")
	if got.State != joinStateAwaitingPayment || got.CheckoutURL != wantURL || got.BankInvoice != nil {
		t.Fatalf("awaiting = %+v, want checkout %s", got, wantURL)
	}
	// Reading the state issues nothing.
	e.joinMe(t, userID, "applicant")
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE user_id = $1`, userID); n != 0 {
		t.Fatalf("fakturas = %d, want 0", n)
	}

	issued := decodeJSON[joinBankInvoiceResponse](t, e.bankInvoice(t, userID))
	got = e.joinMe(t, userID, "applicant")
	if got.State != joinStateBankInvoice || got.BankInvoice == nil || got.CheckoutURL != wantURL {
		t.Fatalf("bank invoice = %+v", got)
	}
	bi := *got.BankInvoice
	if bi.InvoiceID != issued.InvoiceID || bi.KID != issued.KID || bi.Amount != 650 ||
		bi.AccountNumber != "12345678903" || bi.Currency != "NOK" || bi.DueDate != issued.DueDate || bi.Emailed {
		t.Errorf("bank invoice = %+v, want %+v", bi, issued)
	}

	var paymentID string
	if err := e.db.QueryRow(ctx,
		`INSERT INTO payments (club_id, user_id, type, amount, status, paid_at)
		 VALUES ($1, $2, 'dues', 650, 'completed', now()) RETURNING id`,
		e.clubID, userID).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	mustExec(t, e.db, ctx, `UPDATE invoices SET payment_id = $2 WHERE id = $1`, issued.InvoiceID, paymentID)
	if o := membership.GrantForInvoiceInSavepoint(ctx, e.db, issued.InvoiceID, membership.SourceKID); !o.Granted {
		t.Fatalf("grant = %+v", o)
	}
	// The session still says applicant; the state reads the roles afresh.
	if got := e.joinMe(t, userID, "applicant"); got.State != joinStateMember || got.CheckoutURL != "" || got.BankInvoice != nil {
		t.Errorf("after payment = %+v, want member", got)
	}
}

// Anything else is none: an applicant whose join order is gone, and a user
// who neither applied nor is a member.
func TestJoinMeNone(t *testing.T) {
	e := setupBankJoinEnv(t)
	ctx := context.Background()

	userID, orderToken := e.applicant(t, "expired@example.com")
	mustExec(t, e.db, ctx, `UPDATE orders SET status = 'failed', token = NULL WHERE token = $1`, orderToken)
	if got := e.joinMe(t, userID, "applicant"); got.State != joinStateNone || got.CheckoutURL != "" {
		t.Errorf("expired join = %+v, want none", got)
	}

	board := e.seedUser(t, "board@example.com", false, "treasurer")
	if got := e.joinMe(t, board, "treasurer"); got.State != joinStateNone {
		t.Errorf("non-member = %+v, want none", got)
	}
	member := e.seedUser(t, "member@example.com", false, "member")
	if got := e.joinMe(t, member, "member"); got.State != joinStateMember {
		t.Errorf("member = %+v, want member", got)
	}
}

// Each user sees only their own join: another applicant's order and bank
// faktura never leak into the answer.
func TestJoinMeIsolation(t *testing.T) {
	e := setupBankJoinEnv(t)
	ctx := context.Background()

	first, firstToken := e.applicant(t, "first@example.com")
	second, secondToken := e.applicant(t, "second@example.com")
	theirs := decodeJSON[joinBankInvoiceResponse](t, e.bankInvoice(t, second))

	got := e.joinMe(t, first, "applicant")
	if got.State != joinStateAwaitingPayment || got.BankInvoice != nil || got.CheckoutURL != checkoutURL(e.cfg, firstToken) {
		t.Fatalf("first = %+v, want only their own pending order", got)
	}
	if got := e.joinMe(t, second, "applicant"); got.BankInvoice == nil || got.BankInvoice.InvoiceID != theirs.InvoiceID ||
		got.CheckoutURL != checkoutURL(e.cfg, secondToken) {
		t.Fatalf("second = %+v, want their own faktura %s", got, theirs.InvoiceID)
	}

	// With nothing of their own left, the first sees none, not the other's.
	mustExec(t, e.db, ctx, `UPDATE orders SET status = 'failed', token = NULL WHERE token = $1`, firstToken)
	if got := e.joinMe(t, first, "applicant"); got.State != joinStateNone || got.CheckoutURL != "" || got.BankInvoice != nil {
		t.Errorf("first after expiry = %+v, want none", got)
	}
}
