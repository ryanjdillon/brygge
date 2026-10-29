package payments_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/payments"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// fakeRefunder is a provider that can refund, with scripted answers.
type fakeRefunder struct {
	name string

	mu      sync.Mutex
	sent    []payments.RefundRequest
	finds   int
	findErr error
	sendErr error
	result  payments.RefundResult
	// known are refunds the provider has, by request key, for FindRefund.
	known map[string]payments.RefundResult
}

func newFakeRefunder() *fakeRefunder {
	return &fakeRefunder{
		name:   "stripe",
		result: payments.RefundResult{ProviderRefundID: "re_test_1", Status: payments.RefundSucceeded, PaymentRef: "pi_test_1", ChargeRef: "ch_test_1"},
		known:  map[string]payments.RefundResult{},
	}
}

func (f *fakeRefunder) Name() string { return f.name }

func (f *fakeRefunder) CreatePayment(context.Context, payments.Request) (payments.Result, error) {
	return payments.Result{}, nil
}

func (f *fakeRefunder) Refund(_ context.Context, req payments.RefundRequest) (payments.RefundResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, req)
	if f.sendErr != nil {
		return payments.RefundResult{}, f.sendErr
	}
	return f.result, nil
}

func (f *fakeRefunder) FindRefund(_ context.Context, _, requestKey string) (payments.RefundResult, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finds++
	if f.findErr != nil {
		return payments.RefundResult{}, false, f.findErr
	}
	r, ok := f.known[requestKey]
	return r, ok, nil
}

func (f *fakeRefunder) sends() []payments.RefundRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]payments.RefundRequest(nil), f.sent...)
}

// noRefundProvider is a provider without the Refunder interface.
type noRefundProvider struct{}

func (noRefundProvider) Name() string { return "vipps" }
func (noRefundProvider) CreatePayment(context.Context, payments.Request) (payments.Result, error) {
	return payments.Result{}, nil
}

type refundEnv struct {
	pool    *pgxpool.Pool
	svc     *payments.RefundService
	prov    *fakeRefunder
	clubID  string
	actorID string
	orderID string
	attempt string
}

func newRefundEnv(t *testing.T) *refundEnv {
	t.Helper()
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t, testutil.WithMaxConns(10))
	clubID := testutil.SeedClub(t, pool)
	actor, _ := testutil.SeedUser(t, pool, clubID, []string{"treasurer"})
	prov := newFakeRefunder()
	e := &refundEnv{
		pool: pool, prov: prov, clubID: clubID, actorID: actor,
		svc: payments.NewRefundService(pool, payments.NewRegistry(prov, noRefundProvider{}), zerolog.Nop()),
	}
	e.orderID, e.attempt = e.seedPaid(t, "stripe", "captured", 450)
	return e
}

// seedPaid adds an order of the given kr with one attempt.
func (e *refundEnv) seedPaid(t *testing.T, provider, status string, kr int) (orderID, attemptID string) {
	t.Helper()
	ctx := context.Background()
	if err := e.pool.QueryRow(ctx,
		`INSERT INTO orders (club_id, total_amount, token) VALUES ($1, $2, $3) RETURNING id`,
		e.clubID, kr, "tok-"+testutil.RandomHex(8)).Scan(&orderID); err != nil {
		t.Fatalf("order: %v", err)
	}
	e.seedAttemptOn(t, orderID, provider, status, kr)
	if err := e.pool.QueryRow(ctx, `SELECT id::text FROM payment_attempts WHERE order_id = $1`, orderID).Scan(&attemptID); err != nil {
		t.Fatalf("attempt: %v", err)
	}
	return orderID, attemptID
}

func (e *refundEnv) seedAttemptOn(t *testing.T, orderID, provider, status string, kr int) string {
	t.Helper()
	var id string
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id::text`,
		e.clubID, orderID, provider, "cs_test_"+testutil.RandomHex(6), status, kr).Scan(&id); err != nil {
		t.Fatalf("attempt: %v", err)
	}
	return id
}

func (e *refundEnv) input(amountOre int64) payments.RefundInput {
	return payments.RefundInput{
		ClubID: e.clubID, OrderID: e.orderID, AttemptID: e.attempt, AmountOre: amountOre,
		Reason: "Dobbeltbetaling", RequestKey: uuid.NewString(), ActorID: e.actorID,
	}
}

func (e *refundEnv) refundCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM payment_refunds WHERE club_id = $1`, e.clubID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func refusalCode(err error) string {
	var r *payments.RefundRefusal
	if errors.As(err, &r) {
		return r.Code
	}
	return ""
}

// A refund is stored first and sent once: the row carries who asked, why and
// how much, the provider gets the request key and the order, and the answer
// lands on the row.
func TestRefundRequestStoresThenSends(t *testing.T) {
	e := newRefundEnv(t)
	in := e.input(12300)

	out, err := e.svc.Request(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	r := out.Refund
	if out.Replayed || out.Awaiting || out.Rejected != nil || r.Status != payments.RefundSucceeded ||
		r.AmountOre != 12300 || r.Reason != "Dobbeltbetaling" || r.CreatedBy == nil || *r.CreatedBy != e.actorID ||
		r.ProviderRefundID == nil || *r.ProviderRefundID != "re_test_1" || r.Outcome != nil {
		t.Fatalf("outcome = %+v", out)
	}
	sent := e.prov.sends()
	if len(sent) != 1 || sent[0].RequestKey != in.RequestKey || sent[0].AmountOre != 12300 ||
		sent[0].OrderID != e.orderID || sent[0].AttemptID != e.attempt || sent[0].Currency != "NOK" {
		t.Errorf("sent = %+v", sent)
	}
	var payRef, chargeRef string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT provider_payment_ref, charge_ref FROM payment_refunds WHERE id = $1`, r.ID).Scan(&payRef, &chargeRef); err != nil ||
		payRef != "pi_test_1" || chargeRef != "ch_test_1" {
		t.Errorf("refs = %q %q (%v)", payRef, chargeRef, err)
	}
}

// Defaulting to the whole refundable amount, and the amount left shrinking
// as refunds are requested.
func TestRefundDefaultsToRefundable(t *testing.T) {
	e := newRefundEnv(t)
	if _, err := e.svc.Request(context.Background(), e.input(10000)); err != nil {
		t.Fatal(err)
	}
	out, err := e.svc.Request(context.Background(), e.input(0))
	if err != nil {
		t.Fatal(err)
	}
	if out.Refund.AmountOre != 35000 {
		t.Errorf("default amount = %d, want 35000", out.Refund.AmountOre)
	}
	_, err = e.svc.Request(context.Background(), e.input(0))
	if refusalCode(err) != payments.RefundAmountExceeds {
		t.Errorf("third refund: %v, want amount_exceeds_refundable", err)
	}
}

func TestRefundRefusals(t *testing.T) {
	ctx := context.Background()

	t.Run("not captured", func(t *testing.T) {
		e := newRefundEnv(t)
		orderID, attemptID := e.seedPaid(t, "stripe", "authorized", 450)
		in := e.input(100)
		in.OrderID, in.AttemptID = orderID, attemptID
		if _, err := e.svc.Request(ctx, in); refusalCode(err) != payments.RefundNotCaptured {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("provider cannot refund", func(t *testing.T) {
		e := newRefundEnv(t)
		orderID, attemptID := e.seedPaid(t, "vipps", "captured", 450)
		in := e.input(100)
		in.OrderID, in.AttemptID = orderID, attemptID
		if _, err := e.svc.Request(ctx, in); refusalCode(err) != payments.RefundNotSupported {
			t.Errorf("err = %v", err)
		}
		if e.refundCount(t) != 0 {
			t.Error("a refund row was stored")
		}
	})
	t.Run("disputed", func(t *testing.T) {
		e := newRefundEnv(t)
		if _, err := payments.UpsertDispute(ctx, e.pool, payments.Dispute{
			ClubID: e.clubID, Provider: "stripe", ProviderDisputeID: "dp_test_1", AttemptID: e.attempt, Status: "needs_response"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Request(ctx, e.input(100)); refusalCode(err) != payments.RefundDisputed {
			t.Errorf("err = %v", err)
		}
		if len(e.prov.sends()) != 0 {
			t.Error("the provider was called")
		}
	})
	t.Run("more than refundable", func(t *testing.T) {
		e := newRefundEnv(t)
		if _, err := e.svc.Request(ctx, e.input(45001)); refusalCode(err) != payments.RefundAmountExceeds {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a failed refund frees its amount", func(t *testing.T) {
		e := newRefundEnv(t)
		e.prov.sendErr = &payments.RefundRejectedError{Code: "charge_disputed", Message: "no"}
		if out, err := e.svc.Request(ctx, e.input(45000)); err != nil || out.Rejected == nil {
			t.Fatalf("first: %+v, %v", out, err)
		}
		e.prov.sendErr = nil
		if _, err := e.svc.Request(ctx, e.input(45000)); err != nil {
			t.Errorf("second full refund after a failed one: %v", err)
		}
	})
	t.Run("unknown order and attempt", func(t *testing.T) {
		e := newRefundEnv(t)
		in := e.input(100)
		in.OrderID = uuid.NewString()
		if _, err := e.svc.Request(ctx, in); !errors.Is(err, payments.ErrRefundNotFound) {
			t.Errorf("unknown order: %v", err)
		}
		in = e.input(100)
		in.AttemptID = uuid.NewString()
		if _, err := e.svc.Request(ctx, in); !errors.Is(err, payments.ErrRefundNotFound) {
			t.Errorf("unknown attempt: %v", err)
		}
		in = e.input(100)
		in.OrderID = "not-a-uuid"
		if _, err := e.svc.Request(ctx, in); !errors.Is(err, payments.ErrRefundNotFound) {
			t.Errorf("malformed order id: %v", err)
		}
	})
	t.Run("invalid input", func(t *testing.T) {
		e := newRefundEnv(t)
		long := make([]rune, payments.MaxRefundReasonRunes+1)
		for i := range long {
			long[i] = 'å'
		}
		for name, mutate := range map[string]func(*payments.RefundInput){
			"no reason":        func(in *payments.RefundInput) { in.Reason = "  " },
			"long reason":      func(in *payments.RefundInput) { in.Reason = string(long) },
			"negative amount":  func(in *payments.RefundInput) { in.AmountOre = -5 },
			"bad key":          func(in *payments.RefundInput) { in.RequestKey = "nope" },
			"unknown outcome":  func(in *payments.RefundInput) { in.Outcome = "waive" },
			"outcome, no fakt": func(in *payments.RefundInput) { in.Outcome = payments.RefundOutcomeCredit },
		} {
			in := e.input(0)
			mutate(&in)
			if _, err := e.svc.Request(ctx, in); !errors.Is(err, payments.ErrRefundInvalid) {
				t.Errorf("%s: err = %v, want invalid", name, err)
			}
		}
		if e.refundCount(t) != 0 {
			t.Error("a refund row was stored")
		}
	})
}

// The same request key is the same click: the second one returns the row and
// never reaches the provider; reusing the key for another amount or payment
// is refused.
func TestRefundRequestKeyReplay(t *testing.T) {
	e := newRefundEnv(t)
	ctx := context.Background()
	in := e.input(20000)

	first, err := e.svc.Request(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := e.svc.Request(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Replayed || again.Refund.ID != first.Refund.ID || again.Refund.Status != payments.RefundSucceeded {
		t.Errorf("replay = %+v", again)
	}
	if n := len(e.prov.sends()); n != 1 {
		t.Errorf("provider called %d times, want 1", n)
	}
	if n := e.refundCount(t); n != 1 {
		t.Errorf("%d refund rows, want 1", n)
	}

	other := in
	other.AmountOre = 20100
	if _, err := e.svc.Request(ctx, other); refusalCode(err) != payments.RefundRequestKeyReused {
		t.Errorf("same key, other amount: %v", err)
	}
	otherOrder, otherAttempt := e.seedPaid(t, "stripe", "captured", 450)
	other = in
	other.OrderID, other.AttemptID = otherOrder, otherAttempt
	if _, err := e.svc.Request(ctx, other); refusalCode(err) != payments.RefundRequestKeyReused {
		t.Errorf("same key, other payment: %v", err)
	}
	otherReason := in
	otherReason.Reason = "Another reason"
	if _, err := e.svc.Request(ctx, otherReason); refusalCode(err) != payments.RefundRequestKeyReused {
		t.Errorf("same key, other reason: %v", err)
	}
	otherOutcome := in
	otherOutcome.Outcome = payments.RefundOutcomeCredit
	if _, err := e.svc.Request(ctx, otherOutcome); refusalCode(err) != payments.RefundRequestKeyReused {
		t.Errorf("same key, other outcome: %v", err)
	}
	// A key reused with no amount means "the same click".
	omitted := in
	omitted.AmountOre = 0
	if out, err := e.svc.Request(ctx, omitted); err != nil || !out.Replayed {
		t.Errorf("same key, no amount: %+v, %v", out, err)
	}
}

// Two requests over the whole payment with different keys: the order and
// attempt locks let exactly one in.
func TestRefundConcurrentRequestsOneWins(t *testing.T) {
	e := newRefundEnv(t)
	const n = 6
	var wg sync.WaitGroup
	results := make([]error, n)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = e.svc.Request(context.Background(), e.input(45000))
		}()
	}
	wg.Wait()

	var won, refused int
	for _, err := range results {
		switch {
		case err == nil:
			won++
		case refusalCode(err) == payments.RefundAmountExceeds:
			refused++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if won != 1 || refused != n-1 {
		t.Errorf("won %d, refused %d; want 1 and %d", won, refused, n-1)
	}
	if n := len(e.prov.sends()); n != 1 {
		t.Errorf("provider called %d times, want 1", n)
	}
}

// A definite refusal fails the row (with the provider's code); an uncertain
// error leaves it requested, looks once for the refund, and the sweep settles
// it later: found refunds take the provider's state, and a request the
// provider never made is given up after a day.
func TestRefundUncertainThenSweep(t *testing.T) {
	ctx := context.Background()

	t.Run("definite refusal", func(t *testing.T) {
		e := newRefundEnv(t)
		e.prov.sendErr = &payments.RefundRejectedError{Code: "charge_disputed", Message: "disputed"}
		out, err := e.svc.Request(ctx, e.input(1000))
		if err != nil || out.Rejected == nil || out.Refund.Status != payments.RefundFailed ||
			out.Refund.FailureReason == nil || *out.Refund.FailureReason != "charge_disputed" {
			t.Fatalf("outcome = %+v, err = %v", out, err)
		}
	})

	t.Run("uncertain, found by the sweep", func(t *testing.T) {
		e := newRefundEnv(t)
		e.prov.sendErr = errors.New("connection reset")
		in := e.input(1000)
		out, err := e.svc.Request(ctx, in)
		if err != nil || !out.Awaiting || out.Refund.Status != payments.RefundRequested || e.prov.finds != 1 {
			t.Fatalf("outcome = %+v, err = %v, finds = %d", out, err, e.prov.finds)
		}

		// Too young for the sweep.
		if res, err := e.svc.SweepOrder(ctx, e.clubID, e.orderID); err != nil || res.Checked != 0 {
			t.Fatalf("early sweep = %+v, %v", res, err)
		}
		e.age(t, out.Refund.ID, 11*time.Minute)
		if res, err := e.svc.SweepOrder(ctx, e.clubID, e.orderID); err != nil || res.Checked != 1 || res.Settled != 0 || res.Resent != 1 {
			t.Fatalf("sweep without the refund = %+v, %v", res, err)
		}
		e.prov.known[in.RequestKey] = payments.RefundResult{ProviderRefundID: "re_late", Status: payments.RefundSucceeded, PaymentRef: "pi_x", ChargeRef: "ch_x"}
		if res, err := e.svc.SweepRequested(ctx, e.clubID); err != nil || res.Settled != 1 {
			t.Fatalf("sweep with the refund = %+v, %v", res, err)
		}
		got := e.refund(t, out.Refund.ID)
		if got.Status != payments.RefundSucceeded || got.ProviderRefundID == nil || *got.ProviderRefundID != "re_late" {
			t.Errorf("after sweep = %+v", got)
		}
		// The first call and the sweep's resend, both under the one key.
		sends := e.prov.sends()
		if len(sends) != 2 || sends[0].RequestKey != in.RequestKey || sends[1].RequestKey != in.RequestKey {
			t.Errorf("provider calls = %+v, want two under key %s", sends, in.RequestKey)
		}
	})

	t.Run("uncertain, never made", func(t *testing.T) {
		e := newRefundEnv(t)
		e.prov.sendErr = errors.New("timeout")
		out, err := e.svc.Request(ctx, e.input(1000))
		if err != nil || !out.Awaiting {
			t.Fatalf("outcome = %+v, err = %v", out, err)
		}
		e.age(t, out.Refund.ID, 23*time.Hour+30*time.Minute)
		if res, _ := e.svc.SweepRequested(ctx, e.clubID); res.Failed != 0 || res.Resent != 0 {
			t.Fatalf("sweep at 23.5h = %+v", res)
		}
		e.age(t, out.Refund.ID, 25*time.Hour)
		if res, _ := e.svc.SweepRequested(ctx, e.clubID); res.Failed != 1 || res.Resent != 0 {
			t.Fatalf("sweep at 25h = %+v", res)
		}
		if got := e.refund(t, out.Refund.ID); got.Status != payments.RefundFailed {
			t.Errorf("status = %s, want failed", got.Status)
		}
		if n := len(e.prov.sends()); n != 1 {
			t.Errorf("provider called %d times, want 1 (nothing sent past the key's window)", n)
		}
	})

	t.Run("replay of an unconfirmed request looks, never sends", func(t *testing.T) {
		e := newRefundEnv(t)
		e.prov.sendErr = errors.New("timeout")
		in := e.input(1000)
		if _, err := e.svc.Request(ctx, in); err != nil {
			t.Fatal(err)
		}
		e.prov.known[in.RequestKey] = payments.RefundResult{ProviderRefundID: "re_found", Status: payments.RefundRequested}
		out, err := e.svc.Request(ctx, in)
		if err != nil || !out.Replayed || !out.Awaiting || out.Refund.ProviderRefundID == nil || *out.Refund.ProviderRefundID != "re_found" {
			t.Fatalf("replay = %+v, %v", out, err)
		}
		if n := len(e.prov.sends()); n != 1 {
			t.Errorf("provider refunded %d times, want 1", n)
		}
	})
}

func (e *refundEnv) age(t *testing.T, id string, by time.Duration) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE payment_refunds SET created_at = now() - $2::interval WHERE id = $1`, id, by.String()); err != nil {
		t.Fatal(err)
	}
}

func (e *refundEnv) refund(t *testing.T, id string) payments.RefundRecord {
	t.Helper()
	view, err := e.svc.OrderRefundView(context.Background(), e.clubID, e.orderID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range view.Refunds {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("refund %s not in the order's refunds", id)
	return payments.RefundRecord{}
}

// seedFaktura links a faktura to the order the way a faktura payment does.
func (e *refundEnv) seedFaktura(t *testing.T, orderID string) string {
	t.Helper()
	ctx := context.Background()
	var invID string
	if err := e.pool.QueryRow(ctx,
		`INSERT INTO invoices (club_id, invoice_number, kid_number, total_amount, status, issue_date, due_date)
		 VALUES ($1, (SELECT COALESCE(MAX(invoice_number), 0) + 1 FROM invoices WHERE club_id = $1), $2, 450, 'open', CURRENT_DATE, CURRENT_DATE)
		 RETURNING id::text`, e.clubID, "99"+testutil.RandomHex(5)).Scan(&invID); err != nil {
		t.Fatalf("invoice: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO order_sources (order_id, invoice_id) VALUES ($1, $2)`, orderID, invID); err != nil {
		t.Fatalf("order source: %v", err)
	}
	return invID
}

// Credit and recollect apply to the full refund of a faktura's payment; a
// recollect is refused once anything was refunded of it, and neither is
// accepted for a partial refund, a duplicate capture or an order with no
// faktura.
func TestRefundOutcomeRules(t *testing.T) {
	ctx := context.Background()

	t.Run("full credit and recollect", func(t *testing.T) {
		e := newRefundEnv(t)
		e.seedFaktura(t, e.orderID)
		in := e.input(0)
		in.Outcome = payments.RefundOutcomeRecollect
		out, err := e.svc.Request(ctx, in)
		if err != nil || out.Refund.Outcome == nil || *out.Refund.Outcome != payments.RefundOutcomeRecollect {
			t.Fatalf("recollect = %+v, %v", out, err)
		}
	})
	t.Run("partial with an outcome", func(t *testing.T) {
		e := newRefundEnv(t)
		e.seedFaktura(t, e.orderID)
		in := e.input(10000)
		in.Outcome = payments.RefundOutcomeCredit
		if _, err := e.svc.Request(ctx, in); !errors.Is(err, payments.ErrRefundInvalid) {
			t.Errorf("err = %v, want invalid", err)
		}
	})
	t.Run("recollect after a partial refund", func(t *testing.T) {
		e := newRefundEnv(t)
		e.seedFaktura(t, e.orderID)
		if _, err := e.svc.Request(ctx, e.input(10000)); err != nil {
			t.Fatal(err)
		}
		in := e.input(0)
		in.Outcome = payments.RefundOutcomeRecollect
		if _, err := e.svc.Request(ctx, in); refusalCode(err) != payments.RefundRecollectAfterPartly {
			t.Errorf("recollect: %v", err)
		}
		in.Outcome = payments.RefundOutcomeCredit
		in.RequestKey = uuid.NewString()
		if out, err := e.svc.Request(ctx, in); err != nil || out.Refund.AmountOre != 35000 {
			t.Errorf("credit of the rest: %+v, %v", out, err)
		}
	})
	t.Run("recollect after a credit note", func(t *testing.T) {
		e := newRefundEnv(t)
		invID := e.seedFaktura(t, e.orderID)
		e.seedCreditDocument(t, invID)
		in := e.input(0)
		in.Outcome = payments.RefundOutcomeRecollect
		if _, err := e.svc.Request(ctx, in); refusalCode(err) != payments.RefundRecollectAfterPartly {
			t.Errorf("recollect: %v", err)
		}
	})
	t.Run("duplicate capture", func(t *testing.T) {
		e := newRefundEnv(t)
		e.seedFaktura(t, e.orderID)
		dup := e.seedAttemptOn(t, e.orderID, "stripe", "captured", 450)
		in := e.input(0)
		in.AttemptID, in.Outcome = dup, payments.RefundOutcomeCredit
		if _, err := e.svc.Request(ctx, in); !errors.Is(err, payments.ErrRefundInvalid) {
			t.Errorf("err = %v, want invalid", err)
		}
		in.Outcome, in.RequestKey = "", uuid.NewString()
		if out, err := e.svc.Request(ctx, in); err != nil || out.Refund.AttemptID != dup {
			t.Errorf("plain refund of the duplicate: %+v, %v", out, err)
		}
	})
}

// seedCreditDocument records a credit note a refund issued against a faktura.
func (e *refundEnv) seedCreditDocument(t *testing.T, invoiceID string) {
	t.Helper()
	ctx := context.Background()
	var txnID string
	if err := e.pool.QueryRow(ctx,
		`INSERT INTO stripe_balance_transactions
		     (club_id, balance_transaction_id, reporting_category, stripe_type, amount_ore, fee_ore, net_ore, currency, created_at, status)
		 VALUES ($1, $2, 'refund', 'refund', -1000, 0, -1000, 'nok', now(), 'booked') RETURNING id::text`,
		e.clubID, "txn_test_"+testutil.RandomHex(6)).Scan(&txnID); err != nil {
		t.Fatalf("refund txn: %v", err)
	}
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO refund_documents (club_id, refund_txn_id, invoice_id, amount_ore, kind) VALUES ($1, $2, $3, 1000, 'credit')`,
		e.clubID, txnID, invoiceID); err != nil {
		t.Fatalf("refund document: %v", err)
	}
}

// The order's view: refundable per attempt (the duplicate labelled), the
// recollect rule, open disputes, and the bulk answer the lists use.
func TestOrderRefundView(t *testing.T) {
	e := newRefundEnv(t)
	ctx := context.Background()
	e.seedFaktura(t, e.orderID)
	dup := e.seedAttemptOn(t, e.orderID, "stripe", "captured", 450)
	if _, err := e.pool.Exec(ctx,
		`UPDATE payment_attempts SET created_at = created_at + interval '1 minute' WHERE id = $1`, dup); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO stripe_balance_transactions
		     (club_id, balance_transaction_id, reporting_category, stripe_type, amount_ore, fee_ore, net_ore, currency,
		      created_at, status, attempt_id, payment_intent_id)
		 VALUES ($1, 'txn_test_view', 'charge', 'charge', 45000, 0, 45000, 'nok', now(), 'booked', $2, 'pi_test_view')`,
		e.clubID, e.attempt); err != nil {
		t.Fatal(err)
	}
	urls := func(provider, ref string) string { return "https://dash.example.invalid/" + provider + "/" + ref }

	view, err := e.svc.OrderRefundView(ctx, e.clubID, e.orderID, urls)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Attempts) != 2 || !view.CanRefund || !view.RecollectAllowed || !view.HasFakturas {
		t.Fatalf("view = %+v", view)
	}
	primary, second := view.Attempts[0], view.Attempts[1]
	if primary.ID != e.attempt || primary.Duplicate || !primary.RecollectAllowed || primary.Refundable != 450 ||
		primary.PaymentURL != "https://dash.example.invalid/stripe/pi_test_view" {
		t.Errorf("primary = %+v", primary)
	}
	if second.ID != dup || !second.Duplicate || second.RecollectAllowed || !second.CanRefund || second.PaymentURL != "" {
		t.Errorf("duplicate = %+v", second)
	}

	if _, err := e.svc.Request(ctx, e.input(10000)); err != nil {
		t.Fatal(err)
	}
	if _, err := payments.UpsertDispute(ctx, e.pool, payments.Dispute{
		ClubID: e.clubID, Provider: "stripe", ProviderDisputeID: "dp_test_view", AttemptID: dup, Status: "needs_response"}); err != nil {
		t.Fatal(err)
	}
	view, err = e.svc.OrderRefundView(ctx, e.clubID, e.orderID, nil)
	if err != nil {
		t.Fatal(err)
	}
	primary, second = view.Attempts[0], view.Attempts[1]
	if primary.Refunded != 100 || primary.Refundable != 350 || primary.RecollectAllowed || !primary.CanRefund {
		t.Errorf("primary after a partial = %+v", primary)
	}
	if !second.Disputed || second.CanRefund {
		t.Errorf("disputed duplicate = %+v", second)
	}
	if len(view.Refunds) != 1 || len(view.Disputes) != 1 || !view.Disputes[0].Open {
		t.Errorf("refunds %d, disputes %+v", len(view.Refunds), view.Disputes)
	}

	vippsOrder, _ := e.seedPaid(t, "vipps", "captured", 450)
	got, err := e.svc.RefundableOrders(ctx, e.clubID, []string{e.orderID, vippsOrder})
	if err != nil || !got[e.orderID] || got[vippsOrder] {
		t.Errorf("refundable orders = %v, %v", got, err)
	}
	if _, err := e.svc.OrderRefundView(ctx, e.clubID, uuid.NewString(), nil); !errors.Is(err, payments.ErrRefundNotFound) {
		t.Errorf("unknown order: %v", err)
	}
}

// The provider's refunds are recorded whoever made them: a dashboard refund
// gets a row with no person, a button refund is found by its request key and
// learns its refund id, redelivery changes nothing, and a finished refund is
// never put back to requested by a stale update.
func TestUpsertProviderRefund(t *testing.T) {
	e := newRefundEnv(t)
	ctx := context.Background()
	base := payments.ProviderRefund{
		ClubID: e.clubID, Provider: "stripe", AttemptID: e.attempt, ProviderRefundID: "re_dash", AmountOre: 5000,
		Currency: "nok", Status: payments.RefundRequested, PaymentRef: "pi_d", ChargeRef: "ch_d",
	}

	for range 2 {
		if err := payments.UpsertProviderRefund(ctx, e.pool, base); err != nil {
			t.Fatal(err)
		}
	}
	if n := e.refundCount(t); n != 1 {
		t.Fatalf("%d rows after a redelivery, want 1", n)
	}
	row := e.refundByProviderID(t, "re_dash")
	if row.CreatedBy != nil || row.Reason != payments.DashboardRefundReason || row.Outcome != nil ||
		row.AmountOre != 5000 || row.Status != payments.RefundRequested || row.Currency != "NOK" {
		t.Fatalf("dashboard row = %+v", row)
	}

	base.Status = payments.RefundSucceeded
	if err := payments.UpsertProviderRefund(ctx, e.pool, base); err != nil {
		t.Fatal(err)
	}
	base.Status = payments.RefundRequested
	if err := payments.UpsertProviderRefund(ctx, e.pool, base); err != nil {
		t.Fatal(err)
	}
	if got := e.refundByProviderID(t, "re_dash"); got.Status != payments.RefundSucceeded {
		t.Errorf("status after a stale update = %s, want succeeded", got.Status)
	}
	base.Status, base.FailureReason = payments.RefundFailed, "expired_or_canceled_card"
	if err := payments.UpsertProviderRefund(ctx, e.pool, base); err != nil {
		t.Fatal(err)
	}
	if got := e.refundByProviderID(t, "re_dash"); got.Status != payments.RefundFailed || got.FailureReason == nil || *got.FailureReason != "expired_or_canceled_card" {
		t.Errorf("failed refund = %+v", got)
	}

	// A button refund whose answer was lost: found by the key.
	e.prov.sendErr = errors.New("timeout")
	in := e.input(2000)
	in.Outcome = ""
	out, err := e.svc.Request(ctx, in)
	if err != nil || !out.Awaiting {
		t.Fatalf("request = %+v, %v", out, err)
	}
	byKey := payments.ProviderRefund{
		ClubID: e.clubID, Provider: "stripe", AttemptID: e.attempt, ProviderRefundID: "re_button", AmountOre: 2000,
		Currency: "nok", Status: payments.RefundSucceeded, RequestKey: in.RequestKey,
	}
	for range 2 {
		if err := payments.UpsertProviderRefund(ctx, e.pool, byKey); err != nil {
			t.Fatal(err)
		}
	}
	if n := e.refundCount(t); n != 2 {
		t.Fatalf("%d rows, want the dashboard one and the button one", n)
	}
	got := e.refundByProviderID(t, "re_button")
	if got.ID != out.Refund.ID || got.Status != payments.RefundSucceeded || got.CreatedBy == nil || got.Reason != "Dobbeltbetaling" {
		t.Errorf("button row = %+v", got)
	}

	if err := payments.UpsertProviderRefund(ctx, e.pool, payments.ProviderRefund{ClubID: e.clubID, Provider: "stripe"}); err == nil {
		t.Error("a refund with no attempt or id was accepted")
	}
}

func (e *refundEnv) refundByProviderID(t *testing.T, id string) payments.RefundRecord {
	t.Helper()
	view, err := e.svc.OrderRefundView(context.Background(), e.clubID, e.orderID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range view.Refunds {
		if r.ProviderRefundID != nil && *r.ProviderRefundID == id {
			return r
		}
	}
	t.Fatalf("no refund with provider id %s", id)
	return payments.RefundRecord{}
}

// A request that was stored but never reached the provider (a crash between
// the commit and the call, a lost commit answer) does not hold its amount for
// a day: its own key sends it again, once, and never under a new key. A
// request the provider already has is only looked up; one past the key's
// window is not sent at all (PAY-12).
func TestRefundNeverSentRequestIsSentAgainUnderItsKey(t *testing.T) {
	ctx := context.Background()
	stored := func(t *testing.T, e *refundEnv) (payments.RefundInput, string) {
		t.Helper()
		e.prov.sendErr = errors.New("connection reset before the request left")
		in := e.input(20000)
		out, err := e.svc.Request(ctx, in)
		if err != nil || !out.Awaiting {
			t.Fatalf("first request = %+v, %v", out, err)
		}
		e.prov.sendErr = nil
		return in, out.Refund.ID
	}

	t.Run("replay sends it once", func(t *testing.T) {
		e := newRefundEnv(t)
		in, id := stored(t, e)
		for i := 0; i < 2; i++ {
			out, err := e.svc.Request(ctx, in)
			if err != nil || !out.Replayed || out.Refund.Status != payments.RefundSucceeded || out.Refund.ID != id {
				t.Fatalf("replay %d = %+v, %v", i, out, err)
			}
		}
		sends := e.prov.sends()
		if len(sends) != 2 || sends[1].RequestKey != in.RequestKey {
			t.Errorf("provider calls = %+v, want the failed first call and one resend under the same key", sends)
		}
	})

	t.Run("a refund the provider has is not sent again", func(t *testing.T) {
		e := newRefundEnv(t)
		in, _ := stored(t, e)
		e.prov.known[in.RequestKey] = payments.RefundResult{ProviderRefundID: "re_there", Status: payments.RefundRequested}
		out, err := e.svc.Request(ctx, in)
		if err != nil || !out.Replayed || out.Refund.ProviderRefundID == nil || *out.Refund.ProviderRefundID != "re_there" {
			t.Fatalf("replay = %+v, %v", out, err)
		}
		if n := len(e.prov.sends()); n != 1 {
			t.Errorf("provider calls = %d, want only the first", n)
		}
	})

	t.Run("past the window it is not sent", func(t *testing.T) {
		e := newRefundEnv(t)
		in, id := stored(t, e)
		e.age(t, id, 23*time.Hour+30*time.Minute)
		out, err := e.svc.Request(ctx, in)
		if err != nil || !out.Awaiting || out.Refund.Status != payments.RefundRequested {
			t.Fatalf("replay = %+v, %v", out, err)
		}
		if n := len(e.prov.sends()); n != 1 {
			t.Errorf("provider calls = %d, want only the first", n)
		}
	})

	t.Run("a failed look-up sends nothing", func(t *testing.T) {
		e := newRefundEnv(t)
		in, _ := stored(t, e)
		e.prov.findErr = errors.New("list unavailable")
		out, err := e.svc.Request(ctx, in)
		if err != nil || !out.Awaiting {
			t.Fatalf("replay = %+v, %v", out, err)
		}
		if n := len(e.prov.sends()); n != 1 {
			t.Errorf("provider calls = %d, want only the first", n)
		}
	})
}

// One club's refund is never overwritten by another club's event for the same
// provider refund id.
func TestUpsertProviderRefundDoesNotCrossClubs(t *testing.T) {
	e := newRefundEnv(t)
	ctx := context.Background()
	other := testutil.SeedClub(t, e.pool)
	var otherOrder, otherAttempt string
	if err := e.pool.QueryRow(ctx,
		`INSERT INTO orders (club_id, total_amount, token) VALUES ($1, 450, $2) RETURNING id`,
		other, "tok-"+testutil.RandomHex(8)).Scan(&otherOrder); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx,
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount)
		 VALUES ($1, $2, 'stripe', 'cs_other', 'captured', 450) RETURNING id::text`, other, otherOrder).Scan(&otherAttempt); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO payment_refunds (club_id, attempt_id, provider, amount, provider_refund_id, status, reason)
		 VALUES ($1, $2, 'stripe', 10, 're_shared', 'requested', 'Other club')`, other, otherAttempt); err != nil {
		t.Fatal(err)
	}

	err := payments.UpsertProviderRefund(ctx, e.pool, payments.ProviderRefund{
		ClubID: e.clubID, Provider: "stripe", AttemptID: e.attempt, ProviderRefundID: "re_shared", AmountOre: 5000,
		Status: payments.RefundFailed, FailureReason: "expired_or_canceled_card", ChargeRef: "ch_mine",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	var status, reason string
	var charge, failure *string
	if err := e.pool.QueryRow(ctx,
		`SELECT status, reason, charge_ref, failure_reason FROM payment_refunds WHERE club_id = $1 AND provider_refund_id = 're_shared'`,
		other).Scan(&status, &reason, &charge, &failure); err != nil {
		t.Fatal(err)
	}
	if status != "requested" || reason != "Other club" || charge != nil || failure != nil {
		t.Errorf("the other club's refund = %s %q %v %v, want it untouched", status, reason, charge, failure)
	}
	if n := e.refundCount(t); n != 0 {
		t.Errorf("%d rows made for this club", n)
	}
}
