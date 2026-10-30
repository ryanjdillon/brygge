package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// refailInvoice stores the settled debit faktura a failed card refund issues
// in place of faktura's credit note, as the booking pass leaves it.
func (f *creditFixture) refailInvoice(t *testing.T, faktura fin.Invoice, cents int64) fin.Invoice {
	t.Helper()
	f.refundCreditNote(t, faktura, cents)
	debit := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: cents, Paid: true})
	if _, err := f.club.Pool.Exec(context.Background(),
		`INSERT INTO refund_documents (club_id, refund_txn_id, invoice_id, credit_invoice_id, amount_ore, kind)
		 VALUES ($1, $2, $3, $4, $5, 'refail_invoice')`,
		f.club.ID, f.refundRow(t, "refail-pdf-"+debit.ID, cents), faktura.ID, debit.ID, cents); err != nil {
		t.Fatal(err)
	}
	return debit
}

// The PDF of the faktura a failed refund issued says it is paid and shows
// nothing to pay with, whether it is rendered by the delivery worker or
// regenerated later; an ordinary faktura, even a paid one, keeps its payment
// block.
func TestRefailInvoicePDFIsSettled(t *testing.T) {
	f := newCreditFixture(t)
	ctx := context.Background()
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000})
	debit := f.refailInvoice(t, faktura, 45000)
	paidOrdinary := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000, Paid: true})
	club, err := loadClubInvoiceFields(ctx, f.h.db, f.club.ID)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		id   string
		want bool
	}{{"refail faktura", debit.ID, true}, {"faktura", faktura.ID, false}, {"paid faktura", paidOrdinary.ID, false}} {
		st, err := f.h.loadStoredInvoicePDF(ctx, f.club.ID, tc.id, club)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if st.inv.Settled != tc.want || st.inv.CreditNote {
			t.Errorf("%s: Settled, CreditNote = %v, %v; want %v, false", tc.name, st.inv.Settled, st.inv.CreditNote, tc.want)
		}
	}

	if err := NewRefundDocumentSender(f.h).Send(ctx, bcast.Claimed{DeliveryID: "d1", ClubID: f.club.ID, InvoiceID: &debit.ID}); err != nil {
		t.Fatalf("send: %v", err)
	}
	assertSettledLayout := func(stage string) {
		text := f.storedPDFText(t, debit.ID)
		for _, want := range []string{"(Faktura)", "Fakturanummer:", "Status:", "(Betalt)", "Ingen betaling kreves. Fakturaen er allerede betalt."} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: PDF lacks %q", stage, want)
			}
		}
		for _, unwanted := range []string{"Kontonummer", "KID", "Forfallsdato", "Vennligst", "Kreditnota"} {
			if strings.Contains(text, unwanted) {
				t.Errorf("%s: PDF shows %q", stage, unwanted)
			}
		}
	}
	assertSettledLayout("delivery")

	if err := f.h.regenerateOnePDF(ctx, f.club.ID, f.club.Treasurer.ID, "", debit.ID, club, ""); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	assertSettledLayout("regenerate")

	// A KID reissue prints a new KID on an ordinary faktura; here it must
	// not surface on a faktura that asks for nothing.
	if err := f.h.regenerateOnePDF(ctx, f.club.ID, f.club.Treasurer.ID, "", debit.ID, club, "000000999X"); err != nil {
		t.Fatalf("reissue: %v", err)
	}
	assertSettledLayout("reissue")
	if strings.Contains(f.storedPDFText(t, debit.ID), "000000999X") {
		t.Error("reissued KID printed on a settled faktura")
	}

	if err := f.h.regenerateOnePDF(ctx, f.club.ID, f.club.Treasurer.ID, "", paidOrdinary.ID, club, ""); err != nil {
		t.Fatalf("regenerate ordinary: %v", err)
	}
	text := f.storedPDFText(t, paidOrdinary.ID)
	for _, want := range []string{"Kontonummer:", "Forfallsdato:", "Vennligst betal"} {
		if !strings.Contains(text, want) {
			t.Errorf("ordinary paid faktura PDF lacks %q", want)
		}
	}
	if strings.Contains(text, "Betalt") {
		t.Error("ordinary paid faktura PDF says Betalt")
	}
}

// resend runs the Resend action as the treasurer.
func (f *creditFixture) resend(invoiceID string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Post("/invoices/{invoiceID}/resend", f.h.HandleResendInvoice)
	r.Post("/invoices/{invoiceID}/send", f.h.HandleSendInvoice)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/invoices/"+invoiceID+"/resend", nil)
	req = req.WithContext(middleware.WithClaims(req.Context(), f.claims))
	r.ServeHTTP(rec, req)
	return rec
}

// The faktura a failed refund issued is never owed, so no reminder, overdue
// list or pay link picks it up, and an admin who resends it gets the "already
// paid" email, never one with a KID, account or pay link.
func TestRefailInvoiceIsNotOwedAndResendsAsPaid(t *testing.T) {
	f := newCreditFixture(t)
	ctx := context.Background()
	mock := &email.MockSender{}
	f.h.email = mock
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000})
	debit := f.refailInvoice(t, faktura, 45000)
	if f.owed(t, debit) {
		t.Error("the settled debit faktura is owed")
	}
	club, err := loadClubInvoiceFields(ctx, f.h.db, f.club.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{debit.ID, faktura.ID} {
		if _, _, _, err := f.h.ensureInvoicePDF(ctx, f.club.ID, id, club); err != nil {
			t.Fatal(err)
		}
	}

	if rec := f.resend(debit.ID); rec.Code != http.StatusOK {
		t.Fatalf("resend refail faktura = %d %s", rec.Code, rec.Body.String())
	}
	if len(mock.Calls) != 1 {
		t.Fatalf("emails = %d, want one", len(mock.Calls))
	}
	c := mock.Calls[0]
	if !strings.HasPrefix(c.Subject, "Ny faktura fra ") || !strings.Contains(c.HTML, "allerede betalt") {
		t.Errorf("subject %q, body %q; want the refund-document email", c.Subject, c.HTML)
	}
	for _, unwanted := range []string{debit.KID, "href", "Forfall", "KID"} {
		if unwanted != "" && strings.Contains(c.HTML, unwanted) {
			t.Errorf("refail email shows %q: %s", unwanted, c.HTML)
		}
	}

	// An ordinary faktura is resent with its payment details.
	if rec := f.resend(faktura.ID); rec.Code != http.StatusOK {
		t.Fatalf("resend faktura = %d %s", rec.Code, rec.Body.String())
	}
	if len(mock.Calls) != 2 || strings.Contains(mock.Calls[1].HTML, "allerede betalt") {
		t.Fatalf("ordinary faktura resent as a settled one: %+v", mock.Calls)
	}
	if !strings.Contains(mock.Calls[1].HTML, faktura.KID) {
		t.Errorf("ordinary faktura email lacks its KID: %s", mock.Calls[1].HTML)
	}
}

// invoiceMessage takes the refail marker the caller read with the faktura.
func TestInvoiceMessageForSettledFaktura(t *testing.T) {
	h := &InvoiceHandler{}
	due := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	subject, body := h.invoiceMessage(context.Background(), "nb", "club", "inv", "Test Medlem", "Test Båtlag",
		290, due, 450, "000000290X", "1234.56.78901", true)
	if !strings.HasPrefix(subject, "Ny faktura fra Test Båtlag") || !strings.Contains(body, "allerede betalt") {
		t.Errorf("subject %q, body %q", subject, body)
	}
	for _, unwanted := range []string{"000000290X", "1234.56.78901", "31.03.2026", "href"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("email shows %q: %s", unwanted, body)
		}
	}
}
