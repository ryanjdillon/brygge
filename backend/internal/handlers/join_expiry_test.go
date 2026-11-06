package handlers

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// age moves every trace of the user's join (orders, fakturas and their pay
// orders) days into the past.
func (e *bankJoinEnv) age(t *testing.T, userID string, days int) {
	t.Helper()
	ctx := context.Background()
	mustExec(t, e.db, ctx,
		`UPDATE orders SET created_at = now() - make_interval(days => $2), updated_at = now() - make_interval(days => $2)
		  WHERE user_id = $1`, userID, days)
	mustExec(t, e.db, ctx,
		`UPDATE invoices SET created_at = now() - make_interval(days => $2) WHERE user_id = $1`, userID, days)
}

func (e *bankJoinEnv) expire(t *testing.T) JoinExpiryResult {
	t.Helper()
	res, err := e.inv.ExpireUnpaidJoins(context.Background())
	if err != nil {
		t.Fatalf("ExpireUnpaidJoins: %v", err)
	}
	return res
}

func (e *bankJoinEnv) expiredOutcomes(t *testing.T, userID string) []string {
	t.Helper()
	rows, err := e.db.Query(context.Background(),
		`SELECT details->>'outcome' FROM audit_log WHERE resource_id = $1 AND action = $2 ORDER BY created_at`,
		userID, audit.ActionMembershipJoinExpired)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			t.Fatal(err)
		}
		out = append(out, o)
	}
	return out
}

// receivable1500 is what the pair's invoice_sync entries leave on 1500.
func (e *bankJoinEnv) receivable1500(t *testing.T, invoiceIDs ...string) float64 {
	t.Helper()
	var bal float64
	if err := e.db.QueryRow(context.Background(),
		`SELECT COALESCE(sum(jl.debit - jl.credit), 0)::float8
		   FROM journal_lines jl
		   JOIN journal_entries je ON je.id = jl.journal_entry_id
		   JOIN accounts a ON a.id = jl.account_id AND a.code = '1500'
		  WHERE je.source = 'invoice_sync' AND je.source_id = ANY($1) AND je.status = 'posted'`,
		invoiceIDs).Scan(&bal); err != nil {
		t.Fatal(err)
	}
	return bal
}

func (e *bankJoinEnv) syncInvoices(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	period, _, err := e.svc.ResolvePeriodForDate(ctx, e.clubID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SyncInvoices(ctx, e.clubID, period, e.systemActor(t)); err != nil {
		t.Fatalf("SyncInvoices: %v", err)
	}
}

// An applicant never billed is deactivated after 60 days, never deleted; the
// order is expired and unpayable, and the purge erases them on its next run.
func TestJoinExpiryDeactivatesUnbilledApplicant(t *testing.T) {
	e := setupBankJoinEnv(t)
	addr := "gone@example.com"
	userID, orderToken := e.applicant(t, addr)

	e.age(t, userID, 59)
	if res := e.expire(t); res != (JoinExpiryResult{}) {
		t.Fatalf("at 59 days: %+v, want nothing", res)
	}

	e.age(t, userID, 61)
	if res := e.expire(t); res.Deactivated != 1 {
		t.Fatalf("result = %+v, want one deactivated", res)
	}
	if n := e.count(t, `SELECT count(*) FROM users WHERE id = $1 AND deactivated_at IS NOT NULL`, userID); n != 1 {
		t.Error("applicant not deactivated (or deleted)")
	}
	if n := e.count(t, `SELECT count(*) FROM sessions WHERE user_id = $1`, userID); n != 0 {
		t.Errorf("sessions left = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM orders WHERE token = $1`, orderToken); n != 0 {
		t.Error("the join order is still payable by its token")
	}
	if n := e.count(t,
		`SELECT count(*) FROM orders WHERE user_id = $1 AND purpose = 'membership_join' AND status = 'failed'`, userID); n != 1 {
		t.Error("the join order was not expired")
	}
	if got := e.expiredOutcomes(t, userID); !slices.Equal(got, []string{joinExpiredDeactivated}) {
		t.Errorf("audit outcomes = %v", got)
	}
	if res := e.expire(t); res != (JoinExpiryResult{}) {
		t.Errorf("second run = %+v, want nothing", res)
	}

	// No faktura, so the purge erases them at once.
	if _, err := e.svc.PurgeExpiredMemberPII(context.Background()); err != nil {
		t.Fatal(err)
	}
	var email string
	if err := e.db.QueryRow(context.Background(), `SELECT email FROM users WHERE id = $1`, userID).Scan(&email); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(email, accounting.ErasedEmailSuffix) {
		t.Errorf("email after purge = %q, want erased", email)
	}
	if n := e.count(t, `SELECT count(*) FROM membership_applications WHERE lower(email) = $1`, addr); n != 0 {
		t.Errorf("applications left = %d", n)
	}
}

// An issued, unpaid join faktura is credited in full and the applicant
// deactivated; the faktura row stays as it was, numbering runs on, the
// credit note is booked as the faktura is, and the pair nets to zero on 1500.
func TestJoinExpiryCreditsUnpaidFaktura(t *testing.T) {
	e := setupBankJoinEnv(t)
	addr := "credit@example.com"
	userID, _ := e.applicant(t, addr)
	inv := decodeJSON[joinBankInvoiceResponse](t, e.bankInvoice(t, userID))
	e.age(t, userID, 61)
	mailsBefore := len(e.mailsTo(addr))

	if res := e.expire(t); res.Credited != 1 {
		t.Fatalf("result = %+v, want one credited", res)
	}
	var (
		cnID, status      string
		total             float64
		cnNumber, maxNum  int
		cnTotal           float64
		cnSent            bool
		cnUser, cnPurpose *string
	)
	if err := e.db.QueryRow(context.Background(),
		`SELECT cn.id, i.status, i.total_amount::float8, cn.invoice_number, cn.total_amount::float8,
		        cn.sent_at IS NOT NULL, cn.user_id::text, cn.purpose,
		        (SELECT max(invoice_number) FROM invoices WHERE club_id = i.club_id)
		   FROM invoices i JOIN invoices cn ON cn.credits_invoice_id = i.id
		  WHERE i.id = $1`, inv.InvoiceID,
	).Scan(&cnID, &status, &total, &cnNumber, &cnTotal, &cnSent, &cnUser, &cnPurpose, &maxNum); err != nil {
		t.Fatalf("credit note: %v", err)
	}
	if status != "open" || total != 650 {
		t.Errorf("faktura = %s %v, want it unchanged (open, 650)", status, total)
	}
	if cnTotal != -650 || !cnSent || cnUser == nil || *cnUser != userID || cnPurpose != nil {
		t.Errorf("credit note total %v sent %v user %v purpose %v", cnTotal, cnSent, cnUser, cnPurpose)
	}
	if cnNumber != inv.InvoiceNumber+1 || maxNum != cnNumber {
		t.Errorf("credit note number %d (max %d), want %d", cnNumber, maxNum, inv.InvoiceNumber+1)
	}
	fBooked, _ := e.booked(t, inv.InvoiceID)
	cBooked, by := e.booked(t, cnID)
	if !fBooked || !cBooked || by != e.systemActor(t) {
		t.Errorf("booked: faktura %v, credit note %v by %q; want both, by the system actor", fBooked, cBooked, by)
	}
	if bal := e.receivable1500(t, inv.InvoiceID, cnID); bal != 0 {
		t.Errorf("1500 after the credit = %v, want 0", bal)
	}
	e.syncInvoices(t)
	if bal := e.receivable1500(t, inv.InvoiceID, cnID); bal != 0 {
		t.Errorf("1500 after SyncInvoices = %v, want 0", bal)
	}
	if n := e.count(t, `SELECT count(*) FROM users WHERE id = $1 AND deactivated_at IS NOT NULL`, userID); n != 1 {
		t.Error("applicant not deactivated")
	}
	if got := len(e.mailsTo(addr)) - mailsBefore; got != 1 {
		t.Errorf("credit note emails = %d, want 1", got)
	}
	if got := e.expiredOutcomes(t, userID); !slices.Equal(got, []string{joinExpiredCredited}) {
		t.Errorf("audit outcomes = %v", got)
	}

	// Idempotent, and the next faktura takes the next number.
	if res := e.expire(t); res != (JoinExpiryResult{}) {
		t.Errorf("second run = %+v, want nothing", res)
	}
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE club_id = $1`, e.clubID); n != 2 {
		t.Errorf("fakturas = %d, want the faktura and its credit note", n)
	}
	other, _ := e.applicant(t, "next@example.com")
	if next := decodeJSON[joinBankInvoiceResponse](t, e.bankInvoice(t, other)); next.InvoiceNumber != cnNumber+1 {
		t.Errorf("next faktura number = %d, want %d", next.InvoiceNumber, cnNumber+1)
	}
}

// With no ledger at the time, neither the faktura nor its credit note is
// booked; SyncInvoices books both, and the pair nets to zero on 1500.
func TestJoinExpiryCreditUnbookedPairSyncs(t *testing.T) {
	e := setupBankJoinEnv(t)
	e.inv.ledger = nil
	userID, _ := e.applicant(t, "unbooked@example.com")
	inv := decodeJSON[joinBankInvoiceResponse](t, e.bankInvoice(t, userID))
	e.age(t, userID, 61)
	if res := e.expire(t); res.Credited != 1 {
		t.Fatalf("result = %+v", res)
	}
	var cnID string
	if err := e.db.QueryRow(context.Background(),
		`SELECT id FROM invoices WHERE credits_invoice_id = $1`, inv.InvoiceID).Scan(&cnID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{inv.InvoiceID, cnID} {
		if ok, _ := e.booked(t, id); ok {
			t.Fatalf("%s booked without a ledger", id)
		}
	}
	e.syncInvoices(t)
	for _, id := range []string{inv.InvoiceID, cnID} {
		if ok, _ := e.booked(t, id); !ok {
			t.Errorf("%s not booked by SyncInvoices", id)
		}
	}
	if bal := e.receivable1500(t, inv.InvoiceID, cnID); bal != 0 {
		t.Errorf("1500 = %v, want 0", bal)
	}
}

// Part of the money in (a bank row with the faktura's KID): nothing is
// credited, the applicant stays, and the board is told once.
func TestJoinExpiryPartialPaymentAsksTheBoard(t *testing.T) {
	e := setupBankJoinEnv(t)
	ctx := context.Background()
	userID, _ := e.applicant(t, "partial@example.com")
	inv := decodeJSON[joinBankInvoiceResponse](t, e.bankInvoice(t, userID))
	treasurer := e.seedUser(t, "kasserar@example.com", false, "treasurer")
	var importID string
	if err := e.db.QueryRow(ctx,
		`INSERT INTO bank_imports (club_id, filename, imported_by) VALUES ($1, 'test.csv', $2) RETURNING id`, e.clubID, treasurer,
	).Scan(&importID); err != nil {
		t.Fatal(err)
	}
	mustExec(t, e.db, ctx,
		`INSERT INTO bank_import_rows (club_id, bank_import_id, row_date, description, amount, kid_number, row_hash)
		 VALUES ($1, $2, CURRENT_DATE, 'Innbetaling', 300, $3, 'partial-join-row')`, e.clubID, importID, inv.KID)
	e.age(t, userID, 61)

	for i, want := range []int{1, 0} {
		if res := e.expire(t); res.Reviews != want || res.Credited != 0 || res.Deactivated != 0 {
			t.Fatalf("run %d: %+v, want %d review", i+1, res, want)
		}
	}
	if n := e.count(t,
		`SELECT count(*) FROM deliveries WHERE user_id = $1 AND kind = 'membership_grant_withheld'
		    AND payload->>'ref' = 'join_expired:invoice:' || $2`, userID, inv.InvoiceID); n != 1 {
		t.Errorf("board notices = %d, want 1", n)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE resource_id = $1 AND action = $2`,
		userID, audit.ActionMembershipJoinExpiredReview); n != 1 {
		t.Errorf("review audit rows = %d, want 1", n)
	}
	if n := e.count(t, `SELECT count(*) FROM invoices WHERE credits_invoice_id = $1`, inv.InvoiceID); n != 0 {
		t.Error("a part-paid faktura was credited")
	}
	if n := e.count(t, `SELECT count(*) FROM users WHERE id = $1 AND deactivated_at IS NULL`, userID); n != 1 {
		t.Error("a part-paid applicant was deactivated")
	}
}

// A capture of the faktura's pay order and the expiry racing: whichever takes
// the locks first wins outright. The faktura is never both paid into
// membership and credited.
func TestJoinExpiryRacesCapture(t *testing.T) {
	e := setupBankJoinEnv(t)
	ctx := context.Background()
	s := orderSettlement{db: e.db, log: zerolog.Nop()}
	for round := range 6 {
		userID, orderToken := e.applicant(t, fmt.Sprintf("race%d@example.com", round))
		inv := decodeJSON[joinBankInvoiceResponse](t, e.bankInvoice(t, userID))
		ref := fmt.Sprintf("race-%d", round)
		mustExec(t, e.db, ctx,
			`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, amount, created_at)
			 SELECT club_id, id, 'vipps', $2, 650, now() - interval '61 days' FROM orders WHERE token = $1`, orderToken, ref)
		e.age(t, userID, 61)
		a, ok, err := s.attemptByReference(ctx, e.clubID, "vipps", ref)
		if err != nil || !ok {
			t.Fatalf("attempt: %v %v", ok, err)
		}

		var wg sync.WaitGroup
		var capErr, expErr error
		wg.Add(2)
		go func() { defer wg.Done(); capErr = s.markAttemptCaptured(ctx, a, time.Now(), nil) }()
		go func() { defer wg.Done(); _, expErr = e.inv.ExpireUnpaidJoins(ctx) }()
		wg.Wait()
		if capErr != nil || expErr != nil {
			t.Fatalf("round %d: capture %v, expiry %v", round, capErr, expErr)
		}

		member := slices.Contains(e.roles(t, userID), "member")
		credited := e.count(t, `SELECT count(*) FROM invoices WHERE credits_invoice_id = $1`, inv.InvoiceID) == 1
		if member == credited {
			t.Fatalf("round %d: member %v, credited %v; want exactly one", round, member, credited)
		}
	}
}

// Members, other roles, a mixed applicant and a recent join are never
// touched, and each club's join is ended in its own club.
func TestJoinExpiryLeavesOthersAlone(t *testing.T) {
	e := setupBankJoinEnv(t)
	ctx := context.Background()

	recent, _ := e.applicant(t, "recent@example.com")
	mixed, _ := e.applicant(t, "mixed@example.com")
	mustExec(t, e.db, ctx, `INSERT INTO user_roles (user_id, club_id, role) VALUES ($1, $2, 'member')`, mixed, e.clubID)
	e.age(t, mixed, 90)

	member := e.seedUser(t, "member@example.com", false, "member")
	mustExec(t, e.db, ctx,
		`INSERT INTO orders (club_id, user_id, total_amount, token, purpose, created_at, updated_at)
		 VALUES ($1, $2, 650, $3, 'membership_join', now() - interval '90 days', now() - interval '90 days')`,
		e.clubID, member, genCheckoutToken())

	// Another club's expired applicant is ended in that club only.
	otherClub := testutil.SeedClub(t, e.db)
	otherUser, _ := testutil.SeedUser(t, e.db, otherClub, []string{"applicant"})
	mustExec(t, e.db, ctx,
		`INSERT INTO orders (club_id, user_id, total_amount, token, purpose, created_at, updated_at)
		 VALUES ($1, $2, 650, $3, 'membership_join', now() - interval '90 days', now() - interval '90 days')`,
		otherClub, otherUser, genCheckoutToken())

	if res := e.expire(t); res.Deactivated != 1 || res.Credited != 0 {
		t.Fatalf("result = %+v, want only the other club's applicant deactivated", res)
	}
	if n := e.count(t, `SELECT count(*) FROM users WHERE id = $1 AND club_id = $2 AND deactivated_at IS NOT NULL`,
		otherUser, otherClub); n != 1 {
		t.Error("the other club's expired applicant was not deactivated")
	}
	if n := e.count(t,
		`SELECT count(*) FROM orders WHERE club_id = $1 AND user_id = $2 AND status = 'failed'`, otherClub, otherUser); n != 1 {
		t.Error("the other club's join order was not expired in its club")
	}
	for _, id := range []string{recent, mixed, member} {
		if n := e.count(t, `SELECT count(*) FROM users WHERE id = $1 AND deactivated_at IS NULL`, id); n != 1 {
			t.Errorf("user %s was touched", id)
		}
		if n := e.count(t, `SELECT count(*) FROM orders WHERE user_id = $1 AND status = 'pending'`, id); n != 1 {
			t.Errorf("user %s lost their pending order", id)
		}
	}
	if !slices.Equal(e.roles(t, member), []string{"member"}) {
		t.Errorf("member roles = %v", e.roles(t, member))
	}
	assertStatus(t, e.bankInvoice(t, recent), http.StatusCreated)
}

// A capture that still lands on an expired join order is not lost: the
// applicant is deactivated, so the grant is withheld and the board told. A
// join order with no user at all is told about too, naming the order.
func TestJoinExpiredOrderCaptureTellsTheBoard(t *testing.T) {
	e := setupBankJoinEnv(t)
	ctx := context.Background()
	s := orderSettlement{db: e.db, log: zerolog.Nop()}
	capture := func(t *testing.T, orderID, ref string) {
		t.Helper()
		mustExec(t, e.db, ctx,
			`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, amount)
			 SELECT club_id, id, 'vipps', $2, 650 FROM orders WHERE id = $1`, orderID, ref)
		a, ok, err := s.attemptByReference(ctx, e.clubID, "vipps", ref)
		if err != nil || !ok {
			t.Fatalf("attempt: %v %v", ok, err)
		}
		if err := s.markAttemptCaptured(ctx, a, time.Now(), nil); err != nil {
			t.Fatal(err)
		}
	}

	userID, orderToken := e.applicant(t, "late@example.com")
	var orderID string
	if err := e.db.QueryRow(ctx, `SELECT id FROM orders WHERE token = $1`, orderToken).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	e.age(t, userID, 61)
	if res := e.expire(t); res.Deactivated != 1 {
		t.Fatalf("result = %+v", res)
	}
	capture(t, orderID, "late-capture")
	if n := e.count(t,
		`SELECT count(*) FROM deliveries WHERE user_id = $1 AND kind = 'membership_grant_withheld'
		    AND payload->>'ref' = 'order:' || $2`, userID, orderID); n != 1 {
		t.Errorf("board notices for the late capture = %d, want 1", n)
	}

	var orphan string
	if err := e.db.QueryRow(ctx,
		`INSERT INTO orders (club_id, total_amount, token, purpose) VALUES ($1, 650, $2, 'membership_join') RETURNING id`,
		e.clubID, genCheckoutToken()).Scan(&orphan); err != nil {
		t.Fatal(err)
	}
	mustExec(t, e.db, ctx,
		`INSERT INTO order_lines (order_id, price_item_id, name, quantity, unit_price, total_price)
		 VALUES ($1, $2, 'Medlemskontingent', 1, 650, 650)`, orphan, e.priceItemID)
	capture(t, orphan, "orphan-capture")
	if n := e.count(t,
		`SELECT count(*) FROM deliveries WHERE user_id IS NULL AND kind = 'membership_grant_withheld'
		    AND payload->>'ref' = 'order:' || $1`, orphan); n != 1 {
		t.Errorf("board notices for the user-less order = %d, want 1", n)
	}
	if n := e.count(t,
		`SELECT count(*) FROM audit_log WHERE action = 'membership.grant_withheld' AND resource = 'order' AND resource_id = $1`,
		orphan); n != 1 {
		t.Errorf("audit rows for the user-less order = %d, want 1", n)
	}
}
