package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

type glBookingFixture struct {
	club *fin.Club
	h    *InvoiceHandler
}

func newGLBookingFixture(t *testing.T) *glBookingFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := accounting.NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	h := &InvoiceHandler{db: pool, config: &config.Config{}, email: &email.MockSender{}, log: zerolog.Nop()}
	h.SetLedger(svc)
	return &glBookingFixture{club: club, h: h}
}

// draft seeds an unsent faktura with a stored PDF, ready for HandleSendInvoice.
func (f *glBookingFixture) draft(t *testing.T, cents int64) fin.Invoice {
	t.Helper()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Cents: cents, Unsent: true})
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE invoices SET pdf_data = '\x25504446'::bytea WHERE id = $1`, inv.ID,
	); err != nil {
		t.Fatalf("pdf: %v", err)
	}
	return inv
}

func (f *glBookingFixture) do(t *testing.T, fn http.HandlerFunc, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Post("/invoices/{invoiceID}/send", fn)
	r.Post("/invoices/{invoiceID}/void", fn)
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{
		UserID: f.club.Treasurer.ID, ClubID: f.club.ID, Roles: []string{"treasurer"},
	}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func (f *glBookingFixture) entryStatus(t *testing.T, invoiceID string) []string {
	t.Helper()
	rows, err := f.club.Pool.Query(context.Background(),
		`SELECT status FROM journal_entries WHERE source = 'invoice_sync' AND source_id = $1`, invoiceID)
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, s)
	}
	return out
}

func TestSendInvoiceBooksIntoGL(t *testing.T) {
	f := newGLBookingFixture(t)
	inv := f.draft(t, 45000)

	rec := f.do(t, f.h.HandleSendInvoice, "/invoices/"+inv.ID+"/send")
	if rec.Code != http.StatusOK {
		t.Fatalf("send: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		GLBooking *glBooking `json:"gl_booking"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.GLBooking == nil || body.GLBooking.Status != "booked" {
		t.Errorf("gl_booking = %+v (err %v), want booked", body.GLBooking, err)
	}
	if got := f.entryStatus(t, inv.ID); len(got) != 1 || got[0] != "posted" {
		t.Errorf("entries = %v, want one posted", got)
	}
}

func TestSendInvoiceClosedPeriodStillSendsAndReports(t *testing.T) {
	f := newGLBookingFixture(t)
	inv := f.draft(t, 45000)
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE fiscal_periods SET status = 'closed' WHERE id = $1`, f.club.PeriodID); err != nil {
		t.Fatalf("close: %v", err)
	}

	rec := f.do(t, f.h.HandleSendInvoice, "/invoices/"+inv.ID+"/send")
	if rec.Code != http.StatusOK {
		t.Fatalf("send must succeed even when booking can't: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Sent      bool       `json:"sent"`
		GLBooking *glBooking `json:"gl_booking"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if !body.Sent || body.GLBooking == nil || body.GLBooking.Status != "failed" || body.GLBooking.Error == "" {
		t.Errorf("response = %+v, want sent with a failed gl_booking and its reason", body)
	}
	if len(f.h.email.(*email.MockSender).Calls) != 1 {
		t.Errorf("email was not sent")
	}
}

func TestVoidInvoiceReversesBooking(t *testing.T) {
	f := newGLBookingFixture(t)
	inv := f.draft(t, 45000)
	if rec := f.do(t, f.h.HandleSendInvoice, "/invoices/"+inv.ID+"/send"); rec.Code != http.StatusOK {
		t.Fatalf("send: %d", rec.Code)
	}

	rec := f.do(t, f.h.HandleVoidInvoice, "/invoices/"+inv.ID+"/void")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("void: %d %s", rec.Code, rec.Body.String())
	}
	if got := f.entryStatus(t, inv.ID); len(got) != 1 || got[0] != "voided" {
		t.Errorf("entries = %v, want the booking voided (with a posted reversal)", got)
	}
	var reversals int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM journal_entries WHERE club_id = $1 AND source = 'manual' AND status = 'posted'`,
		f.club.ID).Scan(&reversals); err != nil || reversals != 1 {
		t.Errorf("reversals = %d (err %v), want 1", reversals, err)
	}
}

func TestVoidInvoiceRefusedWhenBookedInClosedPeriod(t *testing.T) {
	f := newGLBookingFixture(t)
	inv := f.draft(t, 45000)
	if rec := f.do(t, f.h.HandleSendInvoice, "/invoices/"+inv.ID+"/send"); rec.Code != http.StatusOK {
		t.Fatalf("send: %d", rec.Code)
	}
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE fiscal_periods SET status = 'closed' WHERE id = $1`, f.club.PeriodID); err != nil {
		t.Fatalf("close: %v", err)
	}

	rec := f.do(t, f.h.HandleVoidInvoice, "/invoices/"+inv.ID+"/void")
	if rec.Code != http.StatusConflict {
		t.Fatalf("void in closed period: %d, want 409", rec.Code)
	}
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Code != "period_closed" {
		t.Errorf("code = %q, want period_closed", body.Code)
	}
	var status string
	_ = f.club.Pool.QueryRow(context.Background(), `SELECT status FROM invoices WHERE id = $1`, inv.ID).Scan(&status)
	if status != "open" {
		t.Errorf("invoice status = %q, want still open", status)
	}
}
