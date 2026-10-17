package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/testutil"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

// creditFixture is a club with a ledger, two members and the invoice
// handler, for exercising credit-note links end to end (BRY-342).
type creditFixture struct {
	club   *fin.Club
	svc    *accounting.Service
	h      *InvoiceHandler
	alice  fin.Member
	bjorn  fin.Member
	claims *auth.Claims
}

func newCreditFixture(t *testing.T) *creditFixture {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	svc := accounting.NewService(pool, nil, zerolog.Nop())
	club := fin.NewClub(t, pool, fin.WithKontoplan(svc.SeedKontoplan))
	h := &InvoiceHandler{db: pool, config: &config.Config{}, email: &email.MockSender{}, log: zerolog.Nop()}
	h.SetLedger(svc)
	return &creditFixture{
		club: club, svc: svc, h: h,
		alice:  club.NewMember(t, "member"),
		bjorn:  club.NewMember(t, "member"),
		claims: &auth.Claims{UserID: club.Treasurer.ID, ClubID: club.ID, Roles: []string{"treasurer"}},
	}
}

// book posts the faktura's GL entry, as sending it does.
func (f *creditFixture) book(t *testing.T, inv fin.Invoice) {
	t.Helper()
	if _, err := f.svc.BookInvoice(context.Background(), f.club.ID, inv.ID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("book faktura %d: %v", inv.Number, err)
	}
}

func (f *creditFixture) call(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/invoices/{invoiceID}/credit-candidates", f.h.HandleListCreditCandidates)
	r.Post("/invoices/{invoiceID}/credit-link", f.h.HandleLinkCreditNote)
	r.Delete("/invoices/{invoiceID}/credit-link", f.h.HandleUnlinkCreditNote)
	r.Post("/invoices/{invoiceID}/void", f.h.HandleVoidInvoice)
	r.Get("/invoices", f.h.HandleListInvoices)
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req = req.WithContext(middleware.WithClaims(req.Context(), f.claims))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func (f *creditFixture) link(t *testing.T, creditNote, invoice fin.Invoice) *httptest.ResponseRecorder {
	t.Helper()
	return f.call(t, http.MethodPost, "/invoices/"+creditNote.ID+"/credit-link", map[string]string{"invoice_id": invoice.ID})
}

func (f *creditFixture) owed(t *testing.T, inv fin.Invoice) bool {
	t.Helper()
	var owed bool
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT `+finance.OwedByMemberSQL("i")+` FROM invoices i WHERE i.id = $1`, inv.ID,
	).Scan(&owed); err != nil {
		t.Fatalf("owed %d: %v", inv.Number, err)
	}
	return owed
}

func (f *creditFixture) journalCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM journal_entries WHERE club_id = $1`, f.club.ID,
	).Scan(&n); err != nil {
		t.Fatalf("journal count: %v", err)
	}
	return n
}

func (f *creditFixture) net(t *testing.T, code string) float64 {
	t.Helper()
	var net float64
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(sum(jl.debit) - sum(jl.credit), 0)
		   FROM journal_lines jl
		   JOIN journal_entries je ON je.id = jl.journal_entry_id
		   JOIN accounts a ON a.id = jl.account_id
		  WHERE je.club_id = $1 AND je.status IN ('posted', 'voided') AND a.code = $2`,
		f.club.ID, code,
	).Scan(&net); err != nil {
		t.Fatalf("net %s: %v", code, err)
	}
	return net
}

func wantCode(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, status, rec.Body.String())
	}
	if code == "" {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Code != code {
		t.Errorf("code = %q, want %q (%s)", body.Code, code, rec.Body.String())
	}
}

// A booked multi-line faktura and its booked full credit note: linking
// settles both without posting anything, and the receivable already nets to
// zero through the credit note's own booking.
func TestCreditLinkSettlesBookedPairWithoutPosting(t *testing.T) {
	f := newCreditFixture(t)
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Lines: []fin.Line{
		{Category: "slip_fee", Cents: 250000},
		{Category: "membership", Cents: 45000},
	}})
	credit := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Lines: []fin.Line{
		{Category: "slip_fee", Cents: -250000},
		{Category: "membership", Cents: -45000},
	}})
	f.book(t, faktura)
	f.book(t, credit)
	if got := f.net(t, "1500"); got != 0 {
		t.Fatalf("1500 before link = %v, want 0 (credit note offsets the faktura)", got)
	}
	if !f.owed(t, faktura) || f.owed(t, credit) {
		t.Fatalf("before link: faktura owed=%v credit owed=%v, want true/false", f.owed(t, faktura), f.owed(t, credit))
	}

	cand := f.call(t, http.MethodGet, "/invoices/"+credit.ID+"/credit-candidates", nil)
	if cand.Code != http.StatusOK || !bytes.Contains(cand.Body.Bytes(), []byte(faktura.ID)) {
		t.Fatalf("candidates = %d %s, want the faktura listed", cand.Code, cand.Body.String())
	}

	entries := f.journalCount(t)
	rec := f.link(t, credit, faktura)
	wantCode(t, rec, http.StatusOK, "")
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"outcome":"settled_pair"`)) {
		t.Errorf("outcome = %s, want settled_pair", rec.Body.String())
	}
	if f.journalCount(t) != entries {
		t.Errorf("linking posted journal entries; it must only record the settlement")
	}
	if f.owed(t, faktura) {
		t.Errorf("faktura still owed after its credit note was linked")
	}
	if got := f.net(t, "1500"); got != 0 {
		t.Errorf("1500 after link = %v, want 0", got)
	}

	// Linking again is refused; so is voiding the credit note while linked.
	wantCode(t, f.link(t, credit, faktura), http.StatusConflict, "already_linked")
	wantCode(t, f.call(t, http.MethodPost, "/invoices/"+credit.ID+"/void", nil), http.StatusConflict, "credit_linked")

	// Undoing the link reopens the faktura.
	if rec := f.call(t, http.MethodDelete, "/invoices/"+credit.ID+"/credit-link", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("unlink = %d %s", rec.Code, rec.Body.String())
	}
	if !f.owed(t, faktura) {
		t.Errorf("faktura not owed again after unlinking")
	}
}

func TestCreditLinkRefusals(t *testing.T) {
	f := newCreditFixture(t)

	t.Run("partial credit", func(t *testing.T) {
		faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 100000})
		credit := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: -40000})
		wantCode(t, f.link(t, credit, faktura), http.StatusConflict, "partial_credit")
		if !f.owed(t, faktura) {
			t.Errorf("a refused partial credit settled the faktura")
		}
	})
	t.Run("different payer", func(t *testing.T) {
		faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 60000})
		credit := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.bjorn, Cents: -60000})
		wantCode(t, f.link(t, credit, faktura), http.StatusConflict, "different_payer")
	})
	t.Run("one side booked", func(t *testing.T) {
		faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 70000})
		f.book(t, faktura)
		credit := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: -70000, Imported: true})
		wantCode(t, f.link(t, credit, faktura), http.StatusConflict, "booking_mismatch")
	})
	t.Run("not a credit note", func(t *testing.T) {
		a := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 30000})
		b := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 30000})
		wantCode(t, f.link(t, a, b), http.StatusBadRequest, "")
	})
	t.Run("other club", func(t *testing.T) {
		other := fin.NewClub(t, f.club.Pool, fin.WithKontoplan(f.svc.SeedKontoplan))
		foreign := other.NewInvoice(t, fin.InvoiceOpts{Cents: 50000})
		credit := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: -50000})
		wantCode(t, f.link(t, credit, foreign), http.StatusNotFound, "")
	})
}

// A credit note against an already-paid faktura means the club owes the
// member; the list reports it as credit owed, never as overdue.
func TestCreditLinkToPaidFakturaIsCreditOwed(t *testing.T) {
	f := newCreditFixture(t)
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.bjorn, Cents: 80000, Imported: true, Paid: true,
		IssueDate: time.Date(2025, 10, 16, 0, 0, 0, 0, time.UTC)})
	credit := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.bjorn, Cents: -80000, Imported: true,
		IssueDate: time.Date(2025, 10, 16, 0, 0, 0, 0, time.UTC)})

	rec := f.link(t, credit, faktura)
	wantCode(t, rec, http.StatusOK, "")
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"outcome":"credit_owed"`)) {
		t.Errorf("outcome = %s, want credit_owed", rec.Body.String())
	}

	list := f.call(t, http.MethodGet, "/invoices?status=imported", nil)
	var body struct {
		Items []struct {
			ID           string  `json:"id"`
			CreditState  *string `json:"credit_state"`
			CreditsNo    *int    `json:"credits_invoice_number"`
			CreditedByNo *int    `json:"credited_by_invoice_number"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list: %v (%s)", err, list.Body.String())
	}
	found := 0
	for _, it := range body.Items {
		switch it.ID {
		case credit.ID:
			found++
			if it.CreditState == nil || *it.CreditState != "credit_owed" || it.CreditsNo == nil || *it.CreditsNo != faktura.Number {
				t.Errorf("credit note row = %+v, want credit_owed against faktura %d", it, faktura.Number)
			}
		case faktura.ID:
			found++
			if it.CreditedByNo == nil || *it.CreditedByNo != credit.Number {
				t.Errorf("faktura row = %+v, want credited by %d", it, credit.Number)
			}
		}
	}
	if found != 2 {
		t.Errorf("found %d of the pair in the imported list", found)
	}
}

// Imported 2025 history has no GL on either side; linking settles the pair
// and posts nothing.
func TestCreditLinkImportedUnbookedPair(t *testing.T) {
	f := newCreditFixture(t)
	issued := time.Date(2025, 5, 8, 0, 0, 0, 0, time.UTC)
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 230000, Imported: true, IssueDate: issued})
	credit := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: -230000, Imported: true, IssueDate: issued})

	entries := f.journalCount(t)
	rec := f.link(t, credit, faktura)
	wantCode(t, rec, http.StatusOK, "")
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"outcome":"settled_pair"`)) {
		t.Errorf("outcome = %s, want settled_pair", rec.Body.String())
	}
	if f.journalCount(t) != entries {
		t.Errorf("linking an imported pair posted journal entries")
	}
	if f.owed(t, faktura) || f.owed(t, credit) {
		t.Errorf("imported pair still owed after linking")
	}
}

// Credit notes and credited fakturas never reach reminders or the overview's
// overdue figures; a genuinely owed faktura still does.
func TestCreditNotesAreNeverOverdueOrReminded(t *testing.T) {
	f := newCreditFixture(t)
	owed := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000})
	credited := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.bjorn, Cents: 120000})
	credit := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.bjorn, Cents: -120000})
	unlinked := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: -30000})
	wantCode(t, f.link(t, credit, credited), http.StatusOK, "")
	if _, err := f.club.Pool.Exec(context.Background(),
		`UPDATE invoices SET pdf_data = '\x25504446'::bytea WHERE club_id = $1`, f.club.ID,
	); err != nil {
		t.Fatalf("pdf: %v", err)
	}

	items, res, err := f.h.collectReminderItems(context.Background(), f.club.ID,
		[]string{owed.ID, credited.ID, credit.ID, unlinked.ID}, nil)
	if err != nil {
		t.Fatalf("collectReminderItems: %v", err)
	}
	if len(items) != 1 || items[0].InvoiceID != owed.ID || res.Skipped != 3 {
		t.Errorf("reminders = %+v skipped %d, want only the owed faktura and 3 skipped", items, res.Skipped)
	}

	fh := NewFinancialsHandler(f.club.Pool, &config.Config{}, nil, zerolog.Nop())
	r := chi.NewRouter()
	r.Get("/summary", fh.HandleGetPriceItemSummary)
	req := httptest.NewRequest(http.MethodGet, "/summary", nil)
	req = req.WithContext(middleware.WithClaims(req.Context(), f.claims))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var summary priceItemSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("decode summary: %v (%s)", err, rec.Body.String())
	}
	if summary.Totals.OverdueCount != 1 || summary.Totals.Overdue != 450 {
		t.Errorf("overdue = %d / %v kr, want 1 / 450 (only the owed faktura)",
			summary.Totals.OverdueCount, summary.Totals.Overdue)
	}
}
