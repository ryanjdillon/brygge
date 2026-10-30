package handlers

import (
	"bytes"
	"compress/zlib"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// pdfContentText inflates every content stream of a PDF so tests can look for
// the text drawn on its pages.
func pdfContentText(pdf []byte) string {
	var out strings.Builder
	rest := pdf
	for {
		start := bytes.Index(rest, []byte("stream\n"))
		if start < 0 {
			return out.String()
		}
		rest = rest[start+len("stream\n"):]
		end := bytes.Index(rest, []byte("endstream"))
		if end < 0 {
			return out.String()
		}
		if r, err := zlib.NewReader(bytes.NewReader(rest[:end])); err == nil {
			b, _ := io.ReadAll(r)
			out.Write(b)
		} else {
			out.Write(rest[:end])
		}
		rest = rest[end:]
	}
}

func (f *creditFixture) storedPDFText(t *testing.T, id string) string {
	t.Helper()
	var pdf []byte
	if err := f.club.Pool.QueryRow(context.Background(), `SELECT pdf_data FROM invoices WHERE id = $1`, id).Scan(&pdf); err != nil {
		t.Fatal(err)
	}
	return pdfContentText(pdf)
}

// The PDF of a credit note a card refund issued is a Kreditnota that asks for
// nothing and says the money went back to the card; a faktura beside it is
// rendered as before.
func TestRefundCreditNotePDFAsksForNothing(t *testing.T) {
	f := newCreditFixture(t)
	ctx := context.Background()
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000})
	note := f.refundCreditNote(t, faktura, 9000)

	if err := NewRefundDocumentSender(f.h).Send(ctx, bcast.Claimed{DeliveryID: "d1", ClubID: f.club.ID, InvoiceID: &note.ID}); err != nil {
		t.Fatalf("send: %v", err)
	}
	text := f.storedPDFText(t, note.ID)
	for _, want := range []string{"(Kreditnota)", "Kreditnotanummer:", "Kreditbel", "Ingen betaling kreves.", "refunderes til kortet"} {
		if !strings.Contains(text, want) {
			t.Errorf("credit note PDF lacks %q", want)
		}
	}
	for _, unwanted := range []string{"(Faktura)", "Kontonummer", "KID", "Forfallsdato", "Vennligst"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("credit note PDF shows %q", unwanted)
		}
	}

	club, err := loadClubInvoiceFields(ctx, f.h.db, f.club.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.h.ensureInvoicePDF(ctx, f.club.ID, faktura.ID, club); err != nil {
		t.Fatalf("render faktura: %v", err)
	}
	text = f.storedPDFText(t, faktura.ID)
	for _, want := range []string{"(Faktura)", "Fakturanummer:", "Forfallsdato:"} {
		if !strings.Contains(text, want) {
			t.Errorf("faktura PDF lacks %q", want)
		}
	}
	if strings.Contains(text, "Kreditnota") {
		t.Errorf("faktura PDF shows Kreditnota")
	}
}

// A credit note issued by hand is a Kreditnota too, but nothing says it went
// back to a card: only a refund's credit note does.
func TestStoredPDFInputsMarkCreditNotes(t *testing.T) {
	f := newCreditFixture(t)
	ctx := context.Background()
	club, err := loadClubInvoiceFields(ctx, f.h.db, f.club.ID)
	if err != nil {
		t.Fatal(err)
	}
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000})
	manual := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: -45000})
	refund := f.refundCreditNote(t, faktura, 9000)

	for _, tc := range []struct {
		name                     string
		id                       string
		wantCredit, wantRefunded bool
	}{
		{"faktura", faktura.ID, false, false},
		{"manual credit note", manual.ID, true, false},
		{"refund credit note", refund.ID, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, err := f.h.loadStoredInvoicePDF(ctx, f.club.ID, tc.id, club)
			if err != nil {
				t.Fatal(err)
			}
			if st.inv.CreditNote != tc.wantCredit || st.inv.CardRefund != tc.wantRefunded {
				t.Errorf("CreditNote, CardRefund = %v, %v; want %v, %v",
					st.inv.CreditNote, st.inv.CardRefund, tc.wantCredit, tc.wantRefunded)
			}
		})
	}
}

// The email that carries a credit note, whether sent by the treasurer or
// after a refund, is a credit note email with no payment details or pay link.
func TestInvoiceMessageForCreditNote(t *testing.T) {
	h := &InvoiceHandler{}
	due := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	subject, body := h.invoiceMessage(context.Background(), "nb", "club", "inv", "Test Member", "Test Båtlag",
		289, due, -5, "000000289X", "1234.56.78901", false)
	if !strings.HasPrefix(subject, "Kreditnota fra Test Båtlag") {
		t.Errorf("subject = %q", subject)
	}
	if !strings.Contains(body, "kreditnota #289") {
		t.Errorf("body = %q", body)
	}
	for _, unwanted := range []string{"000000289X", "1234.56.78901", "31.03.2026", "href"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("credit note email shows %q: %s", unwanted, body)
		}
	}
}

func TestInvoiceAttachmentName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		total float64
		want  string
	}{
		{"faktura-0289-nordmann-ola-2026.pdf", 450, "faktura-0289-nordmann-ola-2026.pdf"},
		{"faktura-0289-nordmann-ola-2026.pdf", -5, "kreditnota-0289-nordmann-ola-2026.pdf"},
		{"faktura-289.pdf", -5, "kreditnota-289.pdf"},
		{"faktura-289.pdf", 0, "faktura-289.pdf"},
	} {
		if got := invoiceAttachmentName(tc.name, tc.total); got != tc.want {
			t.Errorf("invoiceAttachmentName(%q, %v) = %q, want %q", tc.name, tc.total, got, tc.want)
		}
	}
}
