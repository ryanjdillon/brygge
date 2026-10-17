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

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil/fin"
)

func (f *creditFixture) void(t *testing.T, inv fin.Invoice) *httptest.ResponseRecorder {
	t.Helper()
	return f.call(t, http.MethodPost, "/invoices/"+inv.ID+"/void", nil)
}

func (f *creditFixture) withAudit() {
	f.h.audit = audit.NewService(f.club.Pool, zerolog.Nop())
}

func (f *creditFixture) status(t *testing.T, inv fin.Invoice) string {
	t.Helper()
	var s string
	if err := f.club.Pool.QueryRow(context.Background(), `SELECT status FROM invoices WHERE id = $1`, inv.ID).Scan(&s); err != nil {
		t.Fatalf("status %d: %v", inv.Number, err)
	}
	return s
}

// voidAudits returns the reversal entry ids of each invoice.voided audit row
// for the faktura.
func (f *creditFixture) voidAudits(t *testing.T, inv fin.Invoice) [][]string {
	t.Helper()
	rows, err := f.club.Pool.Query(context.Background(),
		`SELECT COALESCE(details->'reversal_entry_ids', '[]'::jsonb) FROM audit_log
		  WHERE club_id = $1 AND action = $2 AND resource_id = $3`,
		f.club.ID, audit.ActionInvoiceVoided, inv.ID)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan audit: %v", err)
		}
		var ids []string
		_ = json.Unmarshal(raw, &ids)
		out = append(out, ids)
	}
	return out
}

// Voiding a booked faktura audits invoice.voided with the reversal entry it
// posted, and the receivable nets to zero.
func TestVoidInvoiceAuditsReversalEntries(t *testing.T) {
	f := newCreditFixture(t)
	f.withAudit()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000,
		Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	f.book(t, inv)

	wantCode(t, f.void(t, inv), http.StatusNoContent, "")
	audits := f.voidAudits(t, inv)
	if len(audits) != 1 || len(audits[0]) != 1 {
		t.Fatalf("invoice.voided audits = %v, want one naming one reversal", audits)
	}
	var source string
	if err := f.club.Pool.QueryRow(context.Background(),
		`SELECT source FROM journal_entries WHERE id = $1`, audits[0][0]).Scan(&source); err != nil {
		t.Fatalf("audited reversal %s not found: %v", audits[0][0], err)
	}
	if got := f.net(t, "1500"); got != 0 {
		t.Errorf("1500 net after void = %v, want 0", got)
	}
}

// A faktura settled by a linked credit note can't be voided; the refusal
// names the credit note so the treasurer knows what to unlink.
func TestVoidCreditedFakturaNamesCreditNote(t *testing.T) {
	f := newCreditFixture(t)
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 60000})
	credit := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: -60000})
	wantCode(t, f.link(t, credit, faktura), http.StatusOK, "")

	rec := f.void(t, faktura)
	wantCode(t, rec, http.StatusConflict, "credited")
	var body struct {
		Number int `json:"credit_note_number"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Number != credit.Number {
		t.Errorf("credit_note_number = %d (err %v), want %d", body.Number, err, credit.Number)
	}
	if f.status(t, faktura) == "voided" {
		t.Errorf("credited faktura was voided")
	}
}

// A faktura captured by a Vipps pay link is settled for the member; voiding
// it would hide money already taken.
func TestVoidVippsCapturedFakturaRefused(t *testing.T) {
	f := newCreditFixture(t)
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000, VippsPaid: true})
	if rec := f.void(t, inv); rec.Code != http.StatusConflict {
		t.Fatalf("void of a Vipps-captured faktura = %d %s, want 409", rec.Code, rec.Body.String())
	}
	if f.status(t, inv) == "voided" {
		t.Errorf("Vipps-captured faktura was voided")
	}
}

// A payment landing between the void's checks and its update wins: the void
// reports the change, audits nothing and reverses nothing.
func TestVoidRacingAPaymentReportsChanged(t *testing.T) {
	f := newCreditFixture(t)
	f.withAudit()
	ctx := context.Background()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 45000,
		Lines: []fin.Line{{Category: "membership", Cents: 45000}}})
	f.book(t, inv)
	entries := f.journalCount(t)

	// Hold the faktura row so the void's checks pass and its UPDATE queues
	// behind a payment that commits first.
	tx, err := f.club.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1 FOR UPDATE`, inv.ID); err != nil {
		t.Fatalf("lock: %v", err)
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- f.void(t, inv) }()
	waitForLockWaiter(t, f)

	if _, err := tx.Exec(ctx,
		`WITH p AS (
		    INSERT INTO payments (club_id, user_id, type, amount, status, paid_at)
		    VALUES ($1, $2, 'dues', 450, 'completed', now()) RETURNING id)
		 UPDATE invoices SET payment_id = (SELECT id FROM p) WHERE id = $3`,
		f.club.ID, f.alice.ID, inv.ID); err != nil {
		t.Fatalf("pay: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	wantCode(t, <-done, http.StatusConflict, "changed")
	if f.status(t, inv) == "voided" {
		t.Errorf("paid faktura was voided")
	}
	if n := len(f.voidAudits(t, inv)); n != 0 {
		t.Errorf("%d invoice.voided audit rows for a refused void", n)
	}
	if f.journalCount(t) != entries {
		t.Errorf("a refused void posted a reversal")
	}
}

// waitForLockWaiter blocks until some backend in the test database waits on a
// row lock.
func waitForLockWaiter(t *testing.T, f *creditFixture) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		if err := f.club.Pool.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			                 WHERE datname = current_database() AND wait_event_type = 'Lock')`,
		).Scan(&waiting); err != nil {
			t.Fatalf("pg_stat_activity: %v", err)
		}
		if waiting {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the void never reached its update")
}

// Two credit notes linked to one faktura at once: exactly one wins, the
// other gets already_credited — never two credits against one faktura.
func TestConcurrentCreditLinksOneWins(t *testing.T) {
	f := newCreditFixture(t)
	for round := 0; round < 5; round++ {
		faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 50000})
		notes := []fin.Invoice{
			f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: -50000}),
			f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: -50000}),
		}
		recs := make([]*httptest.ResponseRecorder, len(notes))
		var wg sync.WaitGroup
		for i, cn := range notes {
			wg.Add(1)
			go func() {
				defer wg.Done()
				recs[i] = f.link(t, cn, faktura)
			}()
		}
		wg.Wait()

		ok, conflict := 0, 0
		for _, rec := range recs {
			switch rec.Code {
			case http.StatusOK:
				ok++
			case http.StatusConflict:
				conflict++
				wantCode(t, rec, http.StatusConflict, "already_credited")
			default:
				t.Errorf("round %d: link = %d %s", round, rec.Code, rec.Body.String())
			}
		}
		if ok != 1 || conflict != 1 {
			t.Errorf("round %d: %d linked, %d refused; want exactly one of each", round, ok, conflict)
		}
		var links int
		if err := f.club.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM invoices WHERE credits_invoice_id = $1`, faktura.ID).Scan(&links); err != nil || links != 1 {
			t.Errorf("round %d: %d credit notes linked (err %v), want 1", round, links, err)
		}
	}
}

// A Brygge credit note (booked when sent) can settle an imported faktura that
// never had a GL entry only when the opening balance carried over from the
// previous system put that faktura's receivable on 1500. Without it the
// credit note's CR 1500 would clear a receivable that was never booked.
func TestCreditLinkBookedNoteToImportedFaktura(t *testing.T) {
	f := newCreditFixture(t)
	ctx := context.Background()
	faktura := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 90000, Imported: true,
		IssueDate: time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC)})
	credit := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: -90000})
	f.book(t, credit)

	rec := f.link(t, credit, faktura)
	wantCode(t, rec, http.StatusConflict, "booking_mismatch")
	if !strings.Contains(rec.Body.String(), "opening balance") {
		t.Errorf("refusal should name the missing opening balance: %s", rec.Body.String())
	}

	// Carry the imported receivable over at the cutover: DR 1500 / CR equity.
	src := "opening_balance"
	ob, err := f.svc.CreateJournalEntry(ctx, accounting.CreateJournalEntryInput{
		FiscalPeriodID: f.club.PeriodID,
		EntryDate:      time.Date(f.club.Year, 1, 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"),
		Description:    "Inngående balanse",
		Source:         src,
		CreatedBy:      f.club.Treasurer.ID,
		ClubID:         f.club.ID,
		Lines: []accounting.CreateJournalLineInput{
			{AccountCode: "1500", Debit: 900},
			{AccountCode: "2050", Credit: 900},
		},
	})
	if err != nil {
		t.Fatalf("opening balance: %v", err)
	}
	if err := f.svc.PostJournalEntry(ctx, f.club.ID, ob.ID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("post opening balance: %v", err)
	}

	entries := f.journalCount(t)
	wantCode(t, f.link(t, credit, faktura), http.StatusOK, "")
	if f.journalCount(t) != entries {
		t.Errorf("linking posted a journal entry; the credit note's booking already clears the receivable")
	}
	if f.owed(t, faktura) {
		t.Errorf("imported faktura still owed after its credit note was linked")
	}
	// Opening DR 1500 900 and the credit note's CR 1500 900 cancel out.
	if got := f.net(t, "1500"); got != 0 {
		t.Errorf("1500 net = %v after the link, want 0", got)
	}
}
