package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/brygge-klubb/brygge/internal/email"
)

func (f *exemptionFixture) sendState(t *testing.T, invoiceID string) (sent bool, entries int) {
	t.Helper()
	ctx := context.Background()
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT sent_at IS NOT NULL FROM invoices WHERE id = $1`, invoiceID).Scan(&sent); err != nil {
		t.Fatalf("sent_at: %v", err)
	}
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID).Scan(&entries); err != nil {
		t.Fatalf("journal entries: %v", err)
	}
	return sent, entries
}

// A send whose outcome is unknown may have reached the member, so the
// faktura stays issued and is booked; the caller is still told the outcome
// is unknown. A plain failure rolls back and books nothing.
func TestSendInvoiceAmbiguousKeepsFakturaIssuedAndBooked(t *testing.T) {
	f := newExemptionFixture(t)
	member := f.club.NewMember(t, "member")

	plain := f.createCreditNote(t, member.ID, false)
	ambiguous := f.createCreditNote(t, member.ID, false)
	_, before := f.sendState(t, plain)

	f.mail.Err = errors.New("smtp RCPT TO: 550 no such user")
	if rec := f.callInvoiceAction(t, f.h.HandleSendInvoice, "send", plain); rec.Code != http.StatusBadGateway {
		t.Fatalf("plain failure = %d %s, want 502", rec.Code, rec.Body.String())
	}
	if sent, entries := f.sendState(t, plain); sent || entries != before {
		t.Errorf("plain failure: sent=%v entries %d->%d, want rolled back and nothing booked", sent, before, entries)
	}

	f.mail.Err = fmt.Errorf("smtp close DATA: %w: connection reset", email.ErrDeliveryAmbiguous)
	rec := f.callInvoiceAction(t, f.h.HandleSendInvoice, "send", ambiguous)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("ambiguous = %d %s, want 502", rec.Code, rec.Body.String())
	}
	if want := "email delivery outcome unknown"; !strings.Contains(rec.Body.String(), want) {
		t.Errorf("ambiguous body %s, want %q", rec.Body.String(), want)
	}
	if sent, entries := f.sendState(t, ambiguous); !sent || entries <= before {
		t.Errorf("ambiguous: sent=%v entries %d->%d, want sent_at kept and the faktura booked", sent, before, entries)
	}
}
