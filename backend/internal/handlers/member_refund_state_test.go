package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// The member sees what a refund did to their faktura (refunded in part or in
// full, with the credit notes) and a refund's credit note as such, never as
// credit the club owes, and nothing of the Stripe side or the staff actions.
func TestMemberInvoicesShowRefunds(t *testing.T) {
	f := newCreditFixture(t)
	full := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000, Paid: true})
	note := f.refundCreditNote(t, full, 45000)
	partial := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 100000, Paid: true})
	f.refundCreditNote(t, partial, 25000)
	plain := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 1000})

	mh := &MembersHandler{db: f.club.Pool, config: &config.Config{}, log: zerolog.Nop()}
	req := httptest.NewRequest(http.MethodGet, "/members/me/invoices", nil)
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{
		UserID: f.alice.ID, ClubID: f.club.ID, Roles: []string{"member"},
	}))
	rec := httptest.NewRecorder()
	mh.HandleListMyInvoices(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var got []memberInvoice
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	byID := map[string]memberInvoice{}
	for _, mi := range got {
		byID[mi.ID] = mi
	}
	if r := byID[full.ID]; r.RefundState != "full" || r.RefundedAmount != 450 || !r.Credited || r.Owed ||
		len(r.RefundCreditNotes) != 1 || r.RefundCreditNotes[0].Number != note.Number {
		t.Errorf("fully refunded = %+v", r)
	}
	if r := byID[partial.ID]; r.RefundState != "partial" || r.RefundedAmount != 250 || r.Credited || !r.Paid {
		t.Errorf("partly refunded = %+v", r)
	}
	if r := byID[plain.ID]; r.RefundState != "none" || len(r.RefundCreditNotes) != 0 || !r.Owed {
		t.Errorf("plain = %+v", r)
	}
	if r := byID[note.ID]; !r.RefundDocument || !r.CreditNote || r.Owed || r.RefundState != "none" {
		t.Errorf("refund credit note = %+v, want a credit note marked as a refund's", r)
	}
	if strings.Contains(rec.Body.String(), "dp_") || strings.Contains(rec.Body.String(), "dashboard.stripe.com") {
		t.Errorf("member response leaks the Stripe side: %s", rec.Body)
	}
}
