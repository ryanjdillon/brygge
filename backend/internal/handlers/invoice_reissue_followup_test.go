package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

func (f *creditFixture) reissued(t *testing.T, faktura fin.Invoice) reissueBody {
	t.Helper()
	rec := f.reissue(t, faktura)
	if rec.Code != http.StatusCreated {
		t.Fatalf("reissue = %d %s", rec.Code, rec.Body)
	}
	var got reissueBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// Deleting the draft a "Ny faktura" made clears the link instead of failing
// on it, and the credited faktura can be issued again; so can one whose new
// faktura was voided.
func TestReissueAgainAfterDeleteOrVoid(t *testing.T) {
	f := newCreditFixture(t)
	f.periodForToday(t)
	f.h.email = nil
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000})
	f.refundCreditNote(t, faktura, 45000)

	first := f.reissued(t, faktura)
	if first.SentAt != nil {
		t.Fatal("sent without a mail service")
	}
	if rec := f.call(t, http.MethodDelete, "/invoices/"+first.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("deleting the reissued draft = %d %s, want 204", rec.Code, rec.Body)
	}
	var link *string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT reissued_as_invoice_id::text FROM invoices WHERE id = $1`, faktura.ID).Scan(&link); err != nil || link != nil {
		t.Fatalf("link after delete = %v (%v), want NULL", link, err)
	}
	if row := f.listed(t)[faktura.ID]; !row.CanReissue || row.ReissuedInvoiceNumber != nil {
		t.Errorf("row after delete = %+v, want the action offered again", row)
	}

	second := f.reissued(t, faktura)
	wantCode(t, f.reissue(t, faktura), http.StatusConflict, "already_reissued")

	// Voiding the new faktura frees the credited one again: the list stops
	// naming it and a third can be issued, overwriting the stale link.
	f.h.email = &email.MockSender{}
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE invoices SET status = 'voided' WHERE id = $1`, second.ID); err != nil {
		t.Fatal(err)
	}
	if row := f.listed(t)[faktura.ID]; !row.CanReissue || row.ReissuedInvoiceNumber != nil {
		t.Errorf("row after void = %+v, want the action offered again", row)
	}
	third := f.reissued(t, faktura)
	if third.ID == second.ID {
		t.Error("third reissue reused the voided faktura")
	}
	wantCode(t, f.reissue(t, faktura), http.StatusConflict, "already_reissued")
	if row := f.listed(t)[faktura.ID]; row.ReissuedInvoiceNumber == nil || *row.ReissuedInvoiceNumber != third.InvoiceNumber {
		t.Errorf("row = %+v, want it to name #%d", row, third.InvoiceNumber)
	}
}

// The list and the endpoint apply one rule. A faktura credited by a refund
// that then failed (billed again as a debit faktura) and credited once more
// has a net credit equal to its total, but is not offered and is refused.
func TestReissueRuleIsTheSameInListAndEndpoint(t *testing.T) {
	f := newCreditFixture(t)
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000, Paid: true})
	f.refundCreditNote(t, faktura, 45000)
	debit := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000, Paid: true})
	if _, err := f.club.Pool.Exec(context.Background(),
		`INSERT INTO refund_documents (club_id, refund_txn_id, invoice_id, credit_invoice_id, amount_ore, kind)
		 VALUES ($1, $2, $3, $4, 45000, 'refail_invoice')`, f.club.ID, f.refundRow(t, "rule-refail", 45000), faktura.ID, debit.ID); err != nil {
		t.Fatal(err)
	}
	f.refundCreditNote(t, faktura, 45000)

	row := f.listed(t)[faktura.ID]
	if row.RefundState != "full" || row.CanReissue {
		t.Errorf("row = %+v, want refunded in full and not offered", row)
	}
	wantCode(t, f.reissue(t, faktura), http.StatusConflict, "not_refunded")
}

// Another club's faktura is not found, even for a treasurer.
func TestReissueOfAnotherClubsFaktura(t *testing.T) {
	f := newCreditFixture(t)
	other := fin.NewClub(t, f.club.Pool)
	m := other.NewMember(t, "member")
	foreign := other.NewInvoice(t, fin.InvoiceOpts{Member: &m, Cents: 45000})
	wantCode(t, f.reissue(t, foreign), http.StatusNotFound, "")
	var link *string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT reissued_as_invoice_id::text FROM invoices WHERE id = $1`, foreign.ID).Scan(&link); err != nil || link != nil {
		t.Errorf("foreign faktura touched: %v (%v)", link, err)
	}
}

// A new faktura goes to where the member is now, not to the address the
// credited one was billed to; the payer's current name is used too.
func TestReissueUsesTheMembersCurrentAddress(t *testing.T) {
	f := newCreditFixture(t)
	f.periodForToday(t)
	mock := &email.MockSender{}
	f.h.email = mock
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000})
	f.refundCreditNote(t, faktura, 45000)
	ctx := context.Background()
	if _, err := f.club.Pool.Exec(ctx,
		`UPDATE invoices SET member_email = 'stale@example.test', member_name = 'Stale Name' WHERE id = $1`, faktura.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.club.Pool.Exec(ctx,
		`UPDATE users SET email = 'current@example.test', first_name = 'Current', last_name = 'Name' WHERE id = $1`, f.alice.ID); err != nil {
		t.Fatal(err)
	}

	got := f.reissued(t, faktura)
	if len(mock.Calls) != 1 || mock.Calls[0].To != "current@example.test" {
		t.Fatalf("mails = %+v, want one to the current address", mock.Calls)
	}
	var name, mail string
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT member_name, member_email FROM invoices WHERE id = $1`, got.ID).Scan(&name, &mail); err != nil {
		t.Fatal(err)
	}
	if name != "Current Name" || mail != "current@example.test" {
		t.Errorf("billed to %q <%s>, want the member as they are now", name, mail)
	}

	// An organisation's own recipient address is kept.
	org := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 10000})
	f.refundCreditNote(t, org, 10000)
	if _, err := f.club.Pool.Exec(ctx,
		`UPDATE invoices SET recipient_kind = 'organization', recipient_org_name = 'Synthetic Org AS',
		        recipient_email = 'faktura@org.example.test' WHERE id = $1`, org.ID); err != nil {
		t.Fatal(err)
	}
	mock.Calls = nil
	f.reissued(t, org)
	if len(mock.Calls) != 1 || mock.Calls[0].To != "faktura@org.example.test" {
		t.Errorf("mails = %+v, want one to the organisation's address", mock.Calls)
	}
}

// A line is copied as it was issued ("2 × 500" stays that); one with an
// exemption folded into it is one unit of what the member paid. An exemption
// with no item to fold into is not copied: nothing says what it reduces.
func TestReissueKeepsQuantityAndDropsOrphanExemptions(t *testing.T) {
	f := newCreditFixture(t)
	f.h.email = nil
	ctx := context.Background()
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Lines: []fin.Line{{Category: "slip_fee", Cents: 100000}}})
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.club.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// 2 x 500 for slip_fee; a 100 kr exemption for a price item that is not on the faktura.
	mustExec(`UPDATE invoice_lines SET quantity = 2, unit_price = 500, line_total = 1000 WHERE invoice_id = $1`, faktura.ID)
	mustExec(`INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total, price_item_id, category, exemption_role, account_id)
	          VALUES ($1, 'Fritak', 1, -100, -100, $2, 'mooring', 'chair', (SELECT id FROM accounts WHERE club_id = $3 AND code = '3120'))`,
		faktura.ID, f.club.PriceItem(t, "mooring"), f.club.ID)
	mustExec(`UPDATE invoices SET total_amount = 900 WHERE id = $1`, faktura.ID)
	f.refundCreditNote(t, faktura, 90000)

	got := f.reissued(t, faktura)
	rows, err := f.club.Pool.Query(ctx,
		`SELECT quantity, unit_price::float8, line_total::float8 FROM invoice_lines WHERE invoice_id = $1`, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var qty int
		var unit, total float64
		if err := rows.Scan(&qty, &unit, &total); err != nil {
			t.Fatal(err)
		}
		n++
		if qty != 2 || unit != 500 || total != 1000 {
			t.Errorf("line = %d x %.2f = %.2f, want 2 x 500 = 1000", qty, unit, total)
		}
	}
	if n != 1 {
		t.Errorf("lines = %d, want the one item and no copied exemption", n)
	}
}

// A dispute that names only its payment attempt still shows on the faktura
// the attempt's order paid, for the treasurer; no one else sees it.
func TestListDisputeByAttemptOnly(t *testing.T) {
	f := newCreditFixture(t)
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000, Paid: true})
	order := f.club.NewOrderForInvoice(t, faktura)
	var attemptID string
	if err := f.club.Pool.QueryRow(context.Background(),
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount)
		 VALUES ($1, $2, 'stripe', 'cs_attempt_only', 'captured', 450) RETURNING id`, f.club.ID, order.ID).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.club.Pool.Exec(context.Background(),
		`INSERT INTO payment_disputes (club_id, attempt_id, order_id, provider, provider_dispute_id, status)
		 VALUES ($1, $2, NULL, 'stripe', 'dp_attempt_only', 'needs_response')`, f.club.ID, attemptID); err != nil {
		t.Fatal(err)
	}
	if r := f.listed(t)[faktura.ID]; r.DisputeStatus == nil || *r.DisputeStatus != "needs_response" || r.DisputeURL == "" {
		t.Errorf("treasurer row = %+v, want the dispute", r)
	}
	f.claims.Roles = []string{"board"}
	if r := f.listed(t)[faktura.ID]; r.DisputeStatus != nil || r.DisputeURL != "" {
		t.Errorf("board row = %+v, want no dispute", r)
	}
}
