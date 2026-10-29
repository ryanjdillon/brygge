package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

type reissueBody struct {
	ID                    string  `json:"id"`
	InvoiceNumber         int     `json:"invoice_number"`
	TotalAmount           float64 `json:"total_amount"`
	DueDate               string  `json:"due_date"`
	IssueDate             string  `json:"issue_date"`
	OriginalInvoiceNumber int     `json:"original_invoice_number"`
	SentAt                *string `json:"sent_at"`
}

// periodForToday opens the current year's fiscal period, which a new faktura
// is booked in (the fixture's own year is fixed).
func (f *creditFixture) periodForToday(t *testing.T) {
	t.Helper()
	if _, err := f.club.Pool.Exec(context.Background(),
		`INSERT INTO fiscal_periods (club_id, year, start_date, end_date, status)
		 SELECT $1, $2::int, make_date($2::int, 1, 1), make_date($2::int, 12, 31), 'open'
		  WHERE NOT EXISTS (SELECT 1 FROM fiscal_periods WHERE club_id = $1 AND year = $2::int)`,
		f.club.ID, time.Now().Year()); err != nil {
		t.Fatal(err)
	}
}

func (f *creditFixture) reissue(t *testing.T, faktura fin.Invoice) *httptest.ResponseRecorder {
	t.Helper()
	return f.call(t, http.MethodPost, "/invoices/"+faktura.ID+"/reissue-after-refund", nil)
}

type listedRefund struct {
	ID                    string             `json:"id"`
	CreditState           *string            `json:"credit_state"`
	RefundState           string             `json:"refund_state"`
	RefundedAmount        float64            `json:"refunded_amount"`
	RefundCreditNotes     []refundCreditNote `json:"refund_credit_notes"`
	CanReissue            bool               `json:"can_reissue"`
	ReissuedInvoiceNumber *int               `json:"reissued_invoice_number"`
	ReissueOfNumber       *int               `json:"reissue_of_invoice_number"`
	DisputeStatus         *string            `json:"dispute_status"`
	DisputeURL            string             `json:"dispute_url"`
	PaymentURL            string             `json:"payment_url"`
}

func (f *creditFixture) listed(t *testing.T) map[string]listedRefund {
	t.Helper()
	rec := f.call(t, http.MethodGet, "/invoices?status=sent", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Items []listedRefund `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	out := map[string]listedRefund{}
	for _, it := range body.Items {
		out[it.ID] = it
	}
	return out
}

func (f *creditFixture) lineAmounts(t *testing.T, invoiceID string) []float64 {
	t.Helper()
	rows, err := f.club.Pool.Query(context.Background(),
		`SELECT line_total FROM invoice_lines WHERE invoice_id = $1 ORDER BY line_total`, invoiceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []float64
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

// "Ny faktura" copies the credited faktura to the same payer, sends and books
// it as a faktura made by hand is, and can be done once.
func TestReissueAfterRefund(t *testing.T) {
	f := newCreditFixture(t)
	f.periodForToday(t)
	mock := &email.MockSender{}
	f.h.email = mock
	f.h.audit = audit.NewService(f.club.Pool, zerolog.Nop())
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Lines: []fin.Line{
		{Category: "slip_fee", Cents: 250000}, {Category: "membership", Cents: 45000},
	}})
	f.refundCreditNote(t, faktura, 100000)
	f.refundCreditNote(t, faktura, 195000)

	rec := f.reissue(t, faktura)
	if rec.Code != http.StatusCreated {
		t.Fatalf("reissue = %d %s", rec.Code, rec.Body)
	}
	var got reissueBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.TotalAmount != 2950 || got.OriginalInvoiceNumber != faktura.Number || got.InvoiceNumber == faktura.Number {
		t.Errorf("reissue = %+v, want 2950 kr, a new number, replacing #%d", got, faktura.Number)
	}
	if got.SentAt == nil || len(mock.Calls) != 1 || mock.Calls[0].To != f.alice.Email ||
		!strings.HasPrefix(mock.Calls[0].Filename, "faktura-") {
		t.Errorf("sent = %v, mails = %+v, want the faktura emailed to the payer", got.SentAt, mock.Calls)
	}
	if lines := f.lineAmounts(t, got.ID); len(lines) != 2 || lines[0] != 450 || lines[1] != 2500 {
		t.Errorf("lines = %v, want the original 450 and 2500", lines)
	}

	var userID string
	var sent bool
	var issued, due string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT user_id::text, sent_at IS NOT NULL, issue_date::text, due_date::text FROM invoices WHERE id = $1`, got.ID,
	).Scan(&userID, &sent, &issued, &due); err != nil {
		t.Fatal(err)
	}
	if userID != f.alice.ID || !sent || issued != got.IssueDate || due != got.DueDate || due <= issued {
		t.Errorf("stored: payer %s sent %v issued %s due %s", userID, sent, issued, due)
	}
	// Booked through the send path: DR 1500 for the new faktura.
	if gl := f.net(t, "1500"); gl != 2950 {
		t.Errorf("1500 = %.2f, want the new faktura's 2950 receivable", gl)
	}

	// The new faktura is owed; the credited one stays settled.
	if !f.owed(t, fin.Invoice{ID: got.ID}) || f.owed(t, faktura) {
		t.Errorf("owed: new = %v, credited = %v, want true and false", f.owed(t, fin.Invoice{ID: got.ID}), f.owed(t, faktura))
	}

	// Audited on the credited faktura, naming the new one.
	var details map[string]any
	var actor string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT details, actor_id::text FROM audit_log
		  WHERE club_id = $1 AND action = 'invoice.reissued_after_refund' AND resource_id = $2`,
		f.club.ID, faktura.ID).Scan(&details, &actor); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if actor != f.club.Treasurer.ID || details["new_invoice_id"] != got.ID ||
		int(details["new_invoice_number"].(float64)) != got.InvoiceNumber || details["sent"] != true {
		t.Errorf("audit = %v by %s, want the new faktura named", details, actor)
	}

	// Once only, and the list says so.
	wantCode(t, f.reissue(t, faktura), http.StatusConflict, "already_reissued")
	var audited int
	_ = f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE club_id = $1 AND action = 'invoice.reissued_after_refund'`, f.club.ID).Scan(&audited)
	if audited != 1 {
		t.Errorf("audit rows = %d after a refused second request, want 1", audited)
	}
	row := f.listed(t)[faktura.ID]
	if row.CanReissue || row.ReissuedInvoiceNumber == nil || *row.ReissuedInvoiceNumber != got.InvoiceNumber {
		t.Errorf("credited row = %+v, want reissued as #%d and no second action", row, got.InvoiceNumber)
	}
	if of := f.listed(t)[got.ID].ReissueOfNumber; of == nil || *of != faktura.Number {
		t.Errorf("new row replaces = %v, want #%d", of, faktura.Number)
	}
}

// Only a faktura the refunds credited in full can be issued again.
func TestReissueRefusals(t *testing.T) {
	f := newCreditFixture(t)

	plain := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000})
	wantCode(t, f.reissue(t, plain), http.StatusConflict, "not_refunded")

	partial := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000})
	f.refundCreditNote(t, partial, 9000)
	wantCode(t, f.reissue(t, partial), http.StatusConflict, "not_refunded")

	// Reopened for collection (a recollect refund): already open, nothing to issue.
	recollect := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000})
	if _, err := f.club.Pool.Exec(context.Background(),
		`INSERT INTO refund_documents (club_id, refund_txn_id, invoice_id, amount_ore, kind)
		 VALUES ($1, $2, $3, 45000, 'recollect')`, f.club.ID, f.refundRow(t, "recollect", 45000), recollect.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, f.reissue(t, recollect), http.StatusConflict, "not_refunded")

	voided := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000, Status: "voided"})
	f.refundCreditNote(t, voided, 45000)
	wantCode(t, f.reissue(t, voided), http.StatusConflict, "not_refunded")

	// A credit note, which is not a faktura to collect.
	note := f.refundCreditNote(t, partial, 36000)
	wantCode(t, f.reissue(t, note), http.StatusConflict, "not_refunded")

	// The failed refund brought the money back and re-billed it as a settled
	// debit faktura: billing again would collect twice.
	failed := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000})
	f.refundCreditNote(t, failed, 45000)
	debit := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000, Paid: true})
	if _, err := f.club.Pool.Exec(context.Background(),
		`INSERT INTO refund_documents (club_id, refund_txn_id, invoice_id, credit_invoice_id, amount_ore, kind)
		 VALUES ($1, $2, $3, $4, 45000, 'refail_invoice')`, f.club.ID, f.refundRow(t, "refail", 45000), failed.ID, debit.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, f.reissue(t, failed), http.StatusConflict, "not_refunded")
	if row := f.listed(t)[failed.ID]; row.CanReissue || row.RefundState != "none" {
		t.Errorf("failed-refund row = %+v, want no refund and no action", row)
	}

	wantCode(t, f.call(t, http.MethodPost, "/invoices/not-a-uuid/reissue-after-refund", nil), http.StatusNotFound, "")
	wantCode(t, f.call(t, http.MethodPost, "/invoices/00000000-0000-4000-8000-000000000000/reissue-after-refund", nil), http.StatusNotFound, "")
	if n := f.invoiceCount(t); n != 10 {
		t.Errorf("fakturas = %d, want none issued by a refusal", n)
	}
}

func (f *creditFixture) invoiceCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM invoices WHERE club_id = $1`, f.club.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A role exemption is folded into its item, so the new faktura bills what the
// member paid, and an exempt pair is dropped, not copied as a discount line.
func TestReissueNetsExemptions(t *testing.T) {
	f := newCreditFixture(t)
	f.h.email = nil
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Lines: []fin.Line{
		{Category: "slip_fee", Cents: 100000}, {Category: "membership", Cents: 45000}, {Category: "mooring", Cents: 20000},
	}})
	// A 400 kr exemption on the slip, and the mooring fully exempt.
	for _, ex := range []struct {
		category string
		cents    int64
	}{{"slip_fee", 40000}, {"mooring", 20000}} {
		if _, err := f.club.Pool.Exec(context.Background(),
			`INSERT INTO invoice_lines (invoice_id, description, quantity, unit_price, line_total, price_item_id,
			                            category, exemption_role, account_id)
			 VALUES ($1, 'Fritak', 1, $2, $2, $3, $4, 'chair', (SELECT id FROM accounts WHERE club_id = $5 AND code = '3120'))`,
			faktura.ID, fin.Kr(-ex.cents), f.club.PriceItem(t, ex.category), ex.category, f.club.ID); err != nil {
			t.Fatal(err)
		}
	}
	const paid = 100000 + 45000 + 20000 - 40000 - 20000
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE invoices SET total_amount = $2 WHERE id = $1`, faktura.ID, fin.Kr(paid)); err != nil {
		t.Fatal(err)
	}
	f.refundCreditNote(t, faktura, paid)

	rec := f.reissue(t, faktura)
	if rec.Code != http.StatusCreated {
		t.Fatalf("reissue = %d %s", rec.Code, rec.Body)
	}
	var got reissueBody
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if lines := f.lineAmounts(t, got.ID); len(lines) != 2 || lines[0] != 450 || lines[1] != 600 || got.TotalAmount != 1050 {
		t.Errorf("lines = %v total %.2f, want 450 and 600 (no exemption lines) = 1050", lines, got.TotalAmount)
	}
	if got.SentAt != nil {
		t.Errorf("sent without a mail service: %v", *got.SentAt)
	}
}

// Two clicks at once issue one faktura.
func TestReissueConcurrent(t *testing.T) {
	f := newCreditFixture(t)
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000})
	f.refundCreditNote(t, faktura, 45000)

	codes := make([]int, 4)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = f.serve(http.MethodPost, "/invoices/"+faktura.ID+"/reissue-after-refund", nil).Code
		}()
	}
	wg.Wait()
	created, conflict := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflict++
		}
	}
	if created != 1 || conflict != 3 {
		t.Errorf("codes = %v, want one 201 and three 409", codes)
	}
}

// The list reads a faktura the refunds credited as refunded, a partly
// refunded one as partial and still paid, and links the payment and dispute
// in Stripe for the treasurer only; a refund's credit note is never credit
// the club owes.
func TestListRefundState(t *testing.T) {
	f := newCreditFixture(t)
	full := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000, Paid: true})
	n1 := f.refundCreditNote(t, full, 20000)
	n2 := f.refundCreditNote(t, full, 25000)
	partial := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000, Paid: true})
	note := f.refundCreditNote(t, partial, 9000)
	untouched := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000})

	// The payment and a dispute behind the fully refunded faktura.
	order := f.club.NewOrderForInvoice(t, full)
	var attemptID string
	if err := f.club.Pool.QueryRow(context.Background(),
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount)
		 VALUES ($1, $2, 'stripe', 'cs_synthetic', 'captured', 450) RETURNING id`, f.club.ID, order.ID).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	var refundID string
	if err := f.club.Pool.QueryRow(context.Background(),
		`INSERT INTO payment_refunds (club_id, attempt_id, provider, amount, status, provider_payment_ref)
		 VALUES ($1, $2, 'stripe', 450, 'succeeded', 'pi_3Synthetic0002') RETURNING id`, f.club.ID, attemptID).Scan(&refundID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE refund_documents SET payment_refund_id = $2 WHERE invoice_id = $1`, full.ID, refundID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.club.Pool.Exec(context.Background(),
		`INSERT INTO payment_disputes (club_id, attempt_id, order_id, provider, provider_dispute_id, status)
		 VALUES ($1, $2, $3, 'stripe', 'dp_synthetic1', 'under_review')`, f.club.ID, attemptID, order.ID); err != nil {
		t.Fatal(err)
	}

	rows := f.listed(t)
	state := func(r listedRefund) string {
		if r.CreditState == nil {
			return ""
		}
		return *r.CreditState
	}
	if r := rows[full.ID]; r.RefundState != "full" || r.RefundedAmount != 450 || state(r) != "refunded" || !r.CanReissue ||
		len(r.RefundCreditNotes) != 2 || r.RefundCreditNotes[0].Number != n1.Number || r.RefundCreditNotes[1].Number != n2.Number {
		t.Errorf("fully refunded row = %+v", r)
	}
	if r := rows[partial.ID]; r.RefundState != "partial" || r.RefundedAmount != 90 || state(r) != "" || r.CanReissue ||
		len(r.RefundCreditNotes) != 1 || r.RefundCreditNotes[0].Number != note.Number {
		t.Errorf("partly refunded row = %+v", r)
	}
	if r := rows[untouched.ID]; r.RefundState != "none" || r.RefundedAmount != 0 || r.CanReissue || len(r.RefundCreditNotes) != 0 {
		t.Errorf("untouched row = %+v", r)
	}
	if r := rows[n1.ID]; state(r) != "refund" || r.RefundState != "none" {
		t.Errorf("credit note row = %+v, want state refund (never credit owed)", r)
	}
	if r := rows[full.ID]; !strings.HasSuffix(r.PaymentURL, "/payments/pi_3Synthetic0002") ||
		r.DisputeStatus == nil || *r.DisputeStatus != "under_review" || !strings.HasSuffix(r.DisputeURL, "/disputes/dp_synthetic1") {
		t.Errorf("links = %q %q status %v", r.PaymentURL, r.DisputeURL, r.DisputeStatus)
	}

	// Another role sees the refund states, not the Stripe links, the dispute
	// (it names a member who charged back) or the action.
	f.claims = &auth.Claims{UserID: f.club.Treasurer.ID, ClubID: f.club.ID, Roles: []string{"board"}}
	if r := f.listed(t)[full.ID]; r.CanReissue || r.PaymentURL != "" || r.DisputeURL != "" || r.RefundState != "full" || r.DisputeStatus != nil {
		t.Errorf("board row = %+v", r)
	}
}
