package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	var holder int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holder); err != nil {
		t.Fatalf("holder pid: %v", err)
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- f.serve(http.MethodPost, "/invoices/"+inv.ID+"/void", nil) }()
	if err := waitForBlockedBy(f, holder); err != nil {
		t.Fatal(err)
	}

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

// waitForBlockedBy waits until a backend is blocked by the transaction
// running on holderPID: the void under test, queued on the held row. Keying
// on the holder (rather than "any lock waiter in the database") keeps other
// tests' locks from satisfying it early.
func waitForBlockedBy(f *creditFixture, holderPID int) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		if err := f.club.Pool.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			                 WHERE $1 = ANY (pg_blocking_pids(pid)))`,
			holderPID,
		).Scan(&blocked); err != nil {
			return fmt.Errorf("pg_stat_activity: %w", err)
		}
		if blocked {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("the void never queued behind the held row")
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
				recs[i] = f.serve(http.MethodPost, "/invoices/"+cn.ID+"/credit-link", map[string]string{"invoice_id": faktura.ID})
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

// When one entry of the faktura's family can't be reversed (here the
// resplit correction, after the sync entry's void already ran in the same
// transaction) nothing is reversed: the faktura stays voided, the response
// reports the GL failure, and the audit row names the faktura, no reversal
// entries and the error, so the Bilag repair sync can finish the job.
func TestVoidInvoiceResplitReversalFailureRollsBackFamily(t *testing.T) {
	f := newCreditFixture(t)
	f.withAudit()
	ctx := context.Background()
	inv := f.club.NewInvoice(t, fin.InvoiceOpts{Member: &f.alice, Cents: 60000,
		Lines: []fin.Line{{Category: "membership", Cents: 60000}}})
	f.book(t, inv)

	sourceTable := "invoices"
	resplit, err := f.svc.CreateJournalEntry(ctx, accounting.CreateJournalEntryInput{
		FiscalPeriodID: f.club.PeriodID,
		EntryDate:      time.Now().Format("2006-01-02"),
		Description:    "Synthetic resplit",
		Source:         "invoice_resplit",
		SourceID:       &inv.ID,
		SourceTable:    &sourceTable,
		CreatedBy:      f.club.Treasurer.ID,
		ClubID:         f.club.ID,
		Lines: []accounting.CreateJournalLineInput{
			{AccountCode: "3100", Debit: 100},
			{AccountCode: "3200", Credit: 100},
		},
	})
	if err != nil {
		t.Fatalf("resplit entry: %v", err)
	}
	if err := f.svc.PostJournalEntry(ctx, f.club.ID, resplit.ID, f.club.Treasurer.ID); err != nil {
		t.Fatalf("post resplit: %v", err)
	}
	if _, err := f.club.Pool.Exec(ctx, `
		CREATE FUNCTION refuse_resplit_void() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'synthetic void failure'; END $$;
		CREATE TRIGGER refuse_resplit_void BEFORE UPDATE ON journal_entries FOR EACH ROW
		  WHEN (OLD.id = '`+resplit.ID+`' AND NEW.status = 'voided')
		  EXECUTE FUNCTION refuse_resplit_void();`); err != nil {
		t.Fatalf("fault trigger: %v", err)
	}

	rec := f.void(t, inv)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"failed"`) {
		t.Fatalf("void: status %d body %s, want 200 with gl_booking failed", rec.Code, rec.Body.String())
	}
	if got := f.status(t, inv); got != "voided" {
		t.Errorf("faktura status = %q, want voided", got)
	}
	var posted, reversals int
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE source IN ('invoice_sync', 'invoice_resplit') AND source_id = $2 AND status = 'posted'),
		        count(*) FILTER (WHERE description LIKE 'Reversering av bilag%')
		   FROM journal_entries WHERE club_id = $1`,
		f.club.ID, inv.ID,
	).Scan(&posted, &reversals); err != nil {
		t.Fatalf("entries: %v", err)
	}
	if posted != 2 || reversals != 0 {
		t.Errorf("after a failed family reversal: %d posted family entries, %d reversals; want 2 and 0", posted, reversals)
	}

	var details struct {
		InvoiceNumber    int      `json:"invoice_number"`
		ReversalEntryIDs []string `json:"reversal_entry_ids"`
		GLReversalError  string   `json:"gl_reversal_error"`
	}
	var raw []byte
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT details FROM audit_log WHERE club_id = $1 AND action = $2 AND resource_id = $3`,
		f.club.ID, audit.ActionInvoiceVoided, inv.ID,
	).Scan(&raw); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if err := json.Unmarshal(raw, &details); err != nil {
		t.Fatalf("audit details: %v", err)
	}
	if details.InvoiceNumber != inv.Number || len(details.ReversalEntryIDs) != 0 || details.GLReversalError == "" {
		t.Errorf("audit details = %+v, want invoice_number %d, no reversals, an error", details, inv.Number)
	}
}
