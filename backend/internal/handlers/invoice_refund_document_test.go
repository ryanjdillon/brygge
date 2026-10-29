package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// refundCreditNote stores a credit note a card refund issued for faktura,
// as the booking pass leaves it: a sent credit note linked through
// refund_documents, never through credits_invoice_id.
func (f *creditFixture) refundCreditNote(t *testing.T, faktura fin.Invoice, cents int64) fin.Invoice {
	t.Helper()
	ctx := context.Background()
	note := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: -cents})
	var refundRow string
	if err := f.club.Pool.QueryRow(ctx,
		`INSERT INTO stripe_balance_transactions (club_id, balance_transaction_id, reporting_category, amount_ore,
		                                          net_ore, currency, created_at, status)
		 VALUES ($1, 'txn_test_doc_refund_' || $2::int, 'refund', -$3::bigint, -$3::bigint, 'nok', now(), 'booked')
		 RETURNING id`, f.club.ID, note.Number, cents).Scan(&refundRow); err != nil {
		t.Fatal(err)
	}
	if _, err := f.club.Pool.Exec(ctx,
		`INSERT INTO refund_documents (club_id, refund_txn_id, invoice_id, credit_invoice_id, amount_ore, kind)
		 VALUES ($1, $2, $3, $4, $5, 'credit')`, f.club.ID, refundRow, faktura.ID, note.ID, cents); err != nil {
		t.Fatal(err)
	}
	return note
}

// refundRow stores a booked Stripe refund row for the tests below.
func (f *creditFixture) refundRow(t *testing.T, tag string, cents int64) string {
	t.Helper()
	var id string
	if err := f.club.Pool.QueryRow(context.Background(),
		`INSERT INTO stripe_balance_transactions (club_id, balance_transaction_id, reporting_category, amount_ore,
		                                          net_ore, currency, created_at, status)
		 VALUES ($1, 'txn_test_doc_' || $2::text, 'refund', -$3::bigint, -$3::bigint, 'nok', now(), 'booked')
		 RETURNING id`, f.club.ID, tag, cents).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// A faktura card refunds issued credit notes for takes no manual credit
// note, which would credit it twice: the candidate list leaves it out and
// the link is refused.
func TestManualCreditRefusedOnRefundCreditedFaktura(t *testing.T) {
	f := newCreditFixture(t)
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000})
	f.refundCreditNote(t, faktura, 9000)
	manual := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: -45000})

	rec := f.call(t, http.MethodGet, "/invoices/"+manual.ID+"/credit-candidates?any=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("candidates: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), faktura.ID) {
		t.Errorf("refund-credited faktura offered as a candidate: %s", rec.Body)
	}
	wantCode(t, f.link(t, manual, faktura), http.StatusConflict, "refund_credited")
}

// A voided faktura a refund recorded something against is history: deleting
// it is a 409, not a foreign-key failure.
func TestDeleteVoidedFakturaWithRefundDocuments(t *testing.T) {
	f := newCreditFixture(t)
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000, Status: "voided"})
	row := f.refundRow(t, "recollect", 45000)
	if _, err := f.club.Pool.Exec(context.Background(),
		`INSERT INTO refund_documents (club_id, refund_txn_id, invoice_id, amount_ore, kind)
		 VALUES ($1, $2, $3, 45000, 'recollect')`, f.club.ID, row, faktura.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, f.call(t, http.MethodDelete, "/invoices/"+faktura.ID, nil), http.StatusConflict, "refund_document")

	plain := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 1000, Status: "voided"})
	if rec := f.call(t, http.MethodDelete, "/invoices/"+plain.ID, nil); rec.Code != http.StatusNoContent {
		t.Errorf("deleting a plain voided faktura: %d %s", rec.Code, rec.Body)
	}
}

// A refund's credit note is never offered for, or accepted by, a manual
// credit link, is never voided, and is listed with the faktura it credits.
func TestRefundCreditNoteGuards(t *testing.T) {
	f := newCreditFixture(t)
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000})
	note := f.refundCreditNote(t, faktura, 9000)

	wantCode(t, f.call(t, http.MethodGet, "/invoices/"+note.ID+"/credit-candidates", nil), http.StatusConflict, "refund_document")
	wantCode(t, f.link(t, note, faktura), http.StatusConflict, "refund_document")
	wantCode(t, f.call(t, http.MethodPost, "/invoices/"+note.ID+"/void", nil), http.StatusConflict, "refund_document")

	rec := f.call(t, http.MethodGet, "/invoices?status=sent", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Items []struct {
			ID             string  `json:"id"`
			CreditState    *string `json:"credit_state"`
			CreditsNumber  *int    `json:"credits_invoice_number"`
			RefundDocument bool    `json:"refund_document"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range body.Items {
		if it.ID != note.ID {
			continue
		}
		found = true
		if it.CreditState == nil || *it.CreditState != "refund" || it.CreditsNumber == nil ||
			*it.CreditsNumber != faktura.Number || !it.RefundDocument {
			t.Errorf("credit note row = %+v, want refund of #%d", it, faktura.Number)
		}
	}
	if !found {
		t.Fatal("credit note not listed")
	}
}

// The refund document sender renders the document's PDF and emails it when
// the delivery has an address; without one the PDF is all it does.
func TestRefundDocumentSender(t *testing.T) {
	f := newCreditFixture(t)
	ctx := context.Background()
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000})
	note := f.refundCreditNote(t, faktura, 9000)
	mock := &email.MockSender{}
	f.h.email = mock
	sender := NewRefundDocumentSender(f.h)

	if err := sender.Send(ctx, bcast.Claimed{DeliveryID: "d1", ClubID: f.club.ID, InvoiceID: &note.ID}); err != nil {
		t.Fatalf("send without address: %v", err)
	}
	var stored bool
	if err := f.club.Pool.QueryRow(ctx, `SELECT pdf_data IS NOT NULL FROM invoices WHERE id = $1`, note.ID).Scan(&stored); err != nil || !stored {
		t.Fatalf("PDF stored = %v (%v), want rendered", stored, err)
	}
	if len(mock.Calls) != 0 {
		t.Errorf("emailed without an address: %+v", mock.Calls)
	}

	if err := sender.Send(ctx, bcast.Claimed{DeliveryID: "d2", ClubID: f.club.ID, InvoiceID: &note.ID, Email: f.alice.Email}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(mock.Calls) != 1 || mock.Calls[0].To != f.alice.Email ||
		!strings.HasPrefix(mock.Calls[0].Filename, "kreditnota-") || !strings.Contains(mock.Calls[0].Subject, "Kreditnota") {
		t.Errorf("calls = %+v, want one credit note email", mock.Calls)
	}

	// Without mail configured the row is done once the PDF exists: it must
	// not stay queued to be emailed when SMTP is enabled later.
	f.h.email = nil
	if err := sender.Send(ctx, bcast.Claimed{DeliveryID: "d3", ClubID: f.club.ID, InvoiceID: &note.ID, Email: f.alice.Email}); err != nil {
		t.Errorf("send without mail configured: %v", err)
	}
}
