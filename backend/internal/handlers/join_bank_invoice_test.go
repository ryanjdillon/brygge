package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/membership"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// bankJoinEnv is a club taking joins with an invoice handler for "Betal med
// bank" and the expiry job, sharing the join env's mailer.
type bankJoinEnv struct {
	*joinEnv
	inv *InvoiceHandler
	svc *accounting.Service
}

// setupBankJoinEnv adds the club's chart of accounts and a ledger, so fakturas
// and credit notes are booked as in production.
func setupBankJoinEnv(t *testing.T) *bankJoinEnv {
	t.Helper()
	e := setupJoinEnv(t)
	ctx := context.Background()
	mustExec(t, e.db, ctx, `UPDATE clubs SET bank_account = '12345678903' WHERE id = $1`, e.clubID)
	svc := accounting.NewService(e.db, nil, zerolog.Nop())
	if _, err := svc.SeedKontoplan(ctx, e.clubID); err != nil {
		t.Fatalf("seed kontoplan: %v", err)
	}
	inv := NewInvoiceHandler(e.db, e.cfg, e.mailer, audit.NewService(e.db, zerolog.Nop()), nil, nil, zerolog.Nop())
	inv.SetLedger(svc)
	return &bankJoinEnv{joinEnv: e, inv: inv, svc: svc}
}

// booked reports whether the faktura has a posted invoice_sync entry, and by
// whom.
func (e *bankJoinEnv) booked(t *testing.T, invoiceID string) (bool, string) {
	t.Helper()
	var by string
	err := e.db.QueryRow(context.Background(),
		`SELECT created_by::text FROM journal_entries
		  WHERE source = 'invoice_sync' AND source_id = $1 AND status = 'posted'`, invoiceID).Scan(&by)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return true, by
}

func (e *bankJoinEnv) systemActor(t *testing.T) string {
	t.Helper()
	id, err := e.svc.SystemActor(context.Background(), e.clubID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// applicant joins and confirms addr, and returns the applicant and the join
// order's pay token.
func (e *bankJoinEnv) applicant(t *testing.T, addr string) (userID, orderToken string) {
	t.Helper()
	assertStatus(t, e.do(t, http.MethodPost, "/join", joinBody(addr)), http.StatusAccepted)
	rec := e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": e.lastConfirmToken(t, addr)})
	assertStatus(t, rec, http.StatusOK)
	orderToken = decodeJSON[joinConfirmResponse](t, rec).OrderToken
	if err := e.db.QueryRow(context.Background(),
		`SELECT id FROM users WHERE club_id = $1 AND email = $2`, e.clubID, addr).Scan(&userID); err != nil {
		t.Fatalf("applicant: %v", err)
	}
	return userID, orderToken
}

func (e *bankJoinEnv) bankInvoice(t *testing.T, userID string, userRoles ...string) *httptest.ResponseRecorder {
	t.Helper()
	if len(userRoles) == 0 {
		userRoles = []string{"applicant"}
	}
	r := chi.NewRouter()
	r.Post("/join/bank-invoice", e.inv.HandleJoinBankInvoice)
	req := httptest.NewRequest(http.MethodPost, "/join/bank-invoice", nil)
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: userID, ClubID: e.clubID, Roles: userRoles}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func (e *bankJoinEnv) checkout(t *testing.T, token string) checkoutResp {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/checkout/{token}", e.inv.HandleGetCheckout)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/checkout/"+token, nil))
	assertStatus(t, rec, http.StatusOK)
	return decodeJSON[checkoutResp](t, rec)
}

// "Betal med bank" issues one join faktura from the pending order, once, with
// the order's line and a KID; the order becomes its pay order, so the
// applicant's checkout link and the KID pay the same faktura and nothing
// else is payable.
func TestJoinBankInvoiceIssuedOnce(t *testing.T) {
	e := setupBankJoinEnv(t)
	ctx := context.Background()
	addr := "bank@example.com"
	userID, orderToken := e.applicant(t, addr)
	mailsBefore := len(e.mailsTo(addr))

	rec := e.bankInvoice(t, userID)
	assertStatus(t, rec, http.StatusCreated)
	first := decodeJSON[joinBankInvoiceResponse](t, rec)
	if first.KID == "" || first.Amount != 650 || first.AccountNumber != "12345678903" || first.Currency != "NOK" || !first.Emailed {
		t.Fatalf("response = %+v", first)
	}
	wantDue := time.Now().AddDate(0, 0, defaultFakturaDueDays).Format(time.DateOnly)
	if first.DueDate != wantDue {
		t.Errorf("due date = %s, want %s", first.DueDate, wantDue)
	}

	again := e.bankInvoice(t, userID)
	assertStatus(t, again, http.StatusOK)
	if second := decodeJSON[joinBankInvoiceResponse](t, again); second.InvoiceID != first.InvoiceID || second.KID != first.KID || second.Emailed {
		t.Fatalf("second call = %+v, want the same faktura %+v", second, first)
	}

	var (
		purpose, priceItemID, kid string
		qty, lines                int
		unit, total               float64
	)
	if err := e.db.QueryRow(ctx,
		`SELECT i.purpose, i.kid_number, i.total_amount::float8, l.price_item_id::text, l.quantity, l.unit_price::float8,
		        (SELECT count(*) FROM invoice_lines WHERE invoice_id = i.id)
		   FROM invoices i JOIN invoice_lines l ON l.invoice_id = i.id
		  WHERE i.id = $1 AND i.user_id = $2`, first.InvoiceID, userID,
	).Scan(&purpose, &kid, &total, &priceItemID, &qty, &unit, &lines); err != nil {
		t.Fatal(err)
	}
	if purpose != membership.Purpose || kid != first.KID || total != 650 || priceItemID != e.priceItemID || qty != 1 || unit != 650 || lines != 1 {
		t.Errorf("faktura = purpose %q kid %q total %v item %q qty %d unit %v lines %d", purpose, kid, total, priceItemID, qty, unit, lines)
	}
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE user_id = $1`, userID); n != 1 {
		t.Errorf("fakturas = %d, want 1", n)
	}
	// Issued and booked as the system actor, whatever the email did.
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE id = $1 AND sent_at IS NOT NULL`, first.InvoiceID); n != 1 {
		t.Error("the join faktura is not issued (sent_at)")
	}
	if ok, by := e.booked(t, first.InvoiceID); !ok || by != e.systemActor(t) {
		t.Errorf("booked = %v by %q, want booked by the system actor", ok, by)
	}
	if got := len(e.mailsTo(addr)) - mailsBefore; got != 1 {
		t.Errorf("faktura emails = %d, want 1", got)
	} else if m := e.mailsTo(addr)[len(e.mailsTo(addr))-1]; !strings.Contains(m.HTML, first.KID) {
		t.Errorf("faktura email lacks the KID")
	}

	// One payable thing: the join order is now the faktura's pay order.
	if n := e.count(t,
		`SELECT count(*) FROM orders WHERE user_id = $1 AND purpose IS NOT NULL`, userID); n != 0 {
		t.Errorf("orders with a purpose left = %d, want 0", n)
	}
	if n := e.count(t,
		`SELECT count(*) FROM orders o JOIN order_sources s ON s.order_id = o.id
		  WHERE o.token = $1 AND s.invoice_id = $2 AND NOT EXISTS (SELECT 1 FROM order_lines WHERE order_id = o.id)`,
		orderToken, first.InvoiceID); n != 1 {
		t.Errorf("the join order does not pay the faktura alone")
	}
	co := e.checkout(t, orderToken)
	if co.Total != 650 || co.KID != first.KID || len(co.Lines) != 0 || len(co.Catalog) != 0 || co.Purpose != membership.Purpose {
		t.Errorf("checkout = total %v kid %q lines %d catalog %d purpose %q", co.Total, co.KID, len(co.Lines), len(co.Catalog), co.Purpose)
	}

	// Confirming again points at the faktura's pay order; no second order.
	e.mr.FastForward(joinRecipientGap)
	assertStatus(t, e.do(t, http.MethodPost, "/join", joinBody(addr)), http.StatusAccepted)
	rec = e.do(t, http.MethodPost, "/join/confirm", map[string]string{"token": e.lastConfirmToken(t, addr)})
	assertStatus(t, rec, http.StatusOK)
	if got := decodeJSON[joinConfirmResponse](t, rec).OrderToken; got != orderToken {
		t.Errorf("re-confirm order token = %q, want the faktura's pay order %q", got, orderToken)
	}
	if n := e.count(t, `SELECT count(*) FROM orders WHERE user_id = $1`, userID); n != 1 {
		t.Errorf("orders = %d, want 1", n)
	}
}

func TestJoinBankInvoiceRefusals(t *testing.T) {
	e := setupBankJoinEnv(t)
	member := e.seedUser(t, "member@example.com", false, "member")
	rec := e.bankInvoice(t, member, "member")
	assertStatus(t, rec, http.StatusConflict)
	if code := decodeJSON[map[string]string](t, rec)["code"]; code != codeJoinNotApplicant {
		t.Errorf("member: code = %q", code)
	}

	bare := e.seedUser(t, "bare@example.com", false, "applicant")
	rec = e.bankInvoice(t, bare)
	assertStatus(t, rec, http.StatusConflict)
	if code := decodeJSON[map[string]string](t, rec)["code"]; code != codeJoinNoPending {
		t.Errorf("no order: code = %q", code)
	}
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE club_id = $1`, e.clubID); n != 0 {
		t.Errorf("fakturas = %d, want 0", n)
	}
}

// Paying the join faktura grants membership through BRY-630: by its KID (the
// bank back-link) and by its pay order.
func TestJoinBankInvoicePaymentGrants(t *testing.T) {
	e := setupBankJoinEnv(t)
	ctx := context.Background()

	t.Run("kid", func(t *testing.T) {
		userID, _ := e.applicant(t, "kid@example.com")
		res := decodeJSON[joinBankInvoiceResponse](t, e.bankInvoice(t, userID))
		var paymentID string
		if err := e.db.QueryRow(ctx,
			`INSERT INTO payments (club_id, user_id, type, amount, status, paid_at)
			 VALUES ($1, $2, 'dues', 650, 'completed', now()) RETURNING id`,
			e.clubID, userID).Scan(&paymentID); err != nil {
			t.Fatal(err)
		}
		mustExec(t, e.db, ctx, `UPDATE invoices SET payment_id = $2 WHERE id = $1`, res.InvoiceID, paymentID)
		o := membership.GrantForInvoiceInSavepoint(ctx, e.db, res.InvoiceID, membership.SourceKID)
		if !o.Granted || !slices.Equal(e.roles(t, userID), []string{"member"}) {
			t.Fatalf("outcome = %+v, roles = %v", o, e.roles(t, userID))
		}
	})

	t.Run("pay order", func(t *testing.T) {
		userID, orderToken := e.applicant(t, "online@example.com")
		assertStatus(t, e.bankInvoice(t, userID), http.StatusCreated)
		s := orderSettlement{db: e.db, log: zerolog.Nop()}
		ref := "bank-join-online"
		mustExec(t, e.db, ctx,
			`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, amount)
			 SELECT club_id, id, 'vipps', $2, 650 FROM orders WHERE token = $1`, orderToken, ref)
		a, ok, err := s.attemptByReference(ctx, e.clubID, "vipps", ref)
		if err != nil || !ok {
			t.Fatalf("attempt: %v %v", ok, err)
		}
		if err := s.markAttemptCaptured(ctx, a, time.Now(), nil); err != nil {
			t.Fatal(err)
		}
		if got := e.roles(t, userID); !slices.Equal(got, []string{"member"}) {
			t.Fatalf("roles = %v, want [member]", got)
		}
		if n := e.count(t, `SELECT count(*) FROM deliveries WHERE user_id = $1 AND kind = 'membership_grant_withheld'`, userID); n != 0 {
			t.Errorf("withheld notices = %d, want 0", n)
		}
	})
}

// An online payment of the join order that is reserved, captured or started
// within the provider's session keeps it from becoming a faktura.
func TestJoinBankInvoicePaymentInProgress(t *testing.T) {
	e := setupBankJoinEnv(t)
	ctx := context.Background()
	userID, orderToken := e.applicant(t, "inflight@example.com")
	mustExec(t, e.db, ctx,
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, amount, status)
		 SELECT club_id, id, 'stripe', 'inflight-1', 650, 'created' FROM orders WHERE token = $1`, orderToken)

	refused := func(t *testing.T) {
		t.Helper()
		rec := e.bankInvoice(t, userID)
		assertStatus(t, rec, http.StatusConflict)
		if code := decodeJSON[map[string]string](t, rec)["code"]; code != codeJoinPaymentInProcess {
			t.Fatalf("code = %q", code)
		}
	}
	refused(t)
	mustExec(t, e.db, ctx, `UPDATE payment_attempts SET status = 'authorized' WHERE provider_reference = 'inflight-1'`)
	refused(t)
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE user_id = $1`, userID); n != 0 {
		t.Fatalf("fakturas = %d, want 0", n)
	}

	// An attempt abandoned past the session lifetime is no payment.
	mustExec(t, e.db, ctx,
		`UPDATE payment_attempts SET status = 'created', created_at = now() - make_interval(secs => $1)
		  WHERE provider_reference = 'inflight-1'`, joinPaymentInFlight.Seconds()+60)
	assertStatus(t, e.bankInvoice(t, userID), http.StatusCreated)
}
