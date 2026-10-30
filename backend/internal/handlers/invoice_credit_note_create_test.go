package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// createCreditNote issues a credit note through the single-faktura form: one
// custom line with a negative price.
func (f *exemptionFixture) createCreditNote(t *testing.T, userID string, sendEmail bool) string {
	t.Helper()
	var acct string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT id FROM accounts WHERE club_id = $1 AND code = '3900'`, f.club.ID).Scan(&acct); err != nil {
		t.Fatalf("account: %v", err)
	}
	rec := f.call(t, f.h.HandleCreateInvoice, http.MethodPost, "/invoices/full", createInvoiceFullRequest{
		UserID: userID, DueDate: "2026-06-30", FiscalPeriodID: &f.club.PeriodID, SendEmail: sendEmail,
		Lines: []invoiceLineInput{{Description: "Kreditert avgift", Quantity: 1, UnitPrice: -5, AccountID: &acct}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create credit note = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.ID
}

func (f *exemptionFixture) callInvoiceAction(t *testing.T, fn http.HandlerFunc, action, invoiceID string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Method(http.MethodPost, "/invoices/{invoiceID}/"+action, fn)
	req := httptest.NewRequest(http.MethodPost, "/invoices/"+invoiceID+"/"+action, &bytes.Buffer{})
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{
		UserID: f.club.Treasurer.ID, ClubID: f.club.ID, Roles: []string{"treasurer"},
	}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// A faktura made with a net negative total is a credit note: its stored PDF
// is a Kreditnota with no payment details.
func TestCreateInvoiceWithNegativeTotalStoresCreditNotePDF(t *testing.T) {
	f := newExemptionFixture(t)
	member := f.club.NewMember(t, "member")
	id := f.createCreditNote(t, member.ID, false)

	var pdf []byte
	if err := f.club.Pool.QueryRow(context.Background(), `SELECT pdf_data FROM invoices WHERE id = $1`, id).Scan(&pdf); err != nil {
		t.Fatal(err)
	}
	text := pdfContentText(pdf)
	for _, want := range []string{"(Kreditnota)", "Kreditnotanummer:", "Ingen betaling kreves."} {
		if !strings.Contains(text, want) {
			t.Errorf("credit note PDF lacks %q", want)
		}
	}
	for _, unwanted := range []string{"(Faktura)", "Kontonummer", "KID", "Forfallsdato", "Vennligst"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("credit note PDF shows %q", unwanted)
		}
	}
}

// Creating with send, Send and Resend each email a credit note as one: the
// credit note subject and a kreditnota- attachment, never "ny faktura".
func TestSendCreditNoteEmailsAsCreditNote(t *testing.T) {
	f := newExemptionFixture(t)
	member := f.club.NewMember(t, "member")

	f.createCreditNote(t, member.ID, true)
	id := f.createCreditNote(t, member.ID, false)
	if rec := f.callInvoiceAction(t, f.h.HandleSendInvoice, "send", id); rec.Code != http.StatusOK {
		t.Fatalf("send = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.callInvoiceAction(t, f.h.HandleResendInvoice, "resend", id); rec.Code != http.StatusOK {
		t.Fatalf("resend = %d %s", rec.Code, rec.Body.String())
	}

	if len(f.mail.Calls) != 3 {
		t.Fatalf("emails = %d, want create-and-send, send and resend", len(f.mail.Calls))
	}
	for i, c := range f.mail.Calls {
		if !strings.HasPrefix(c.Subject, "Kreditnota fra ") || !strings.HasPrefix(c.Filename, "kreditnota-") {
			t.Errorf("email %d subject %q, attachment %q; want a credit note", i, c.Subject, c.Filename)
		}
		if strings.Contains(c.HTML, "KID") || strings.Contains(c.HTML, "href") {
			t.Errorf("email %d carries payment details: %s", i, c.HTML)
		}
	}
}
