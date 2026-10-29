package payments_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brygge-klubb/brygge/internal/payments"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

func seedAttempt(t *testing.T, pool *pgxpool.Pool, clubID string) string {
	t.Helper()
	ctx := context.Background()
	var orderID, attemptID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO orders (club_id, total_amount, token) VALUES ($1, 450, $2) RETURNING id`,
		clubID, "tok-"+testutil.RandomHex(8)).Scan(&orderID); err != nil {
		t.Fatalf("order: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO payment_attempts (club_id, order_id, provider, provider_reference, status, amount)
		 VALUES ($1, $2, 'stripe', $3, 'captured', 450) RETURNING id`,
		clubID, orderID, "cs_test_"+testutil.RandomHex(6)).Scan(&attemptID); err != nil {
		t.Fatalf("attempt: %v", err)
	}
	return attemptID
}

type storedDispute struct {
	Status, Reason, Currency, Charge string
	AmountKr                         *float64
	Due                              *time.Time
	Attempt                          *string
}

func readDispute(t *testing.T, pool *pgxpool.Pool, id string) storedDispute {
	t.Helper()
	var d storedDispute
	if err := pool.QueryRow(context.Background(),
		`SELECT status, reason, currency, COALESCE(charge_ref, ''), amount::float8, evidence_due_by, attempt_id::text
		   FROM payment_disputes WHERE provider = 'stripe' AND provider_dispute_id = $1`, id,
	).Scan(&d.Status, &d.Reason, &d.Currency, &d.Charge, &d.AmountKr, &d.Due, &d.Attempt); err != nil {
		t.Fatalf("dispute %s: %v", id, err)
	}
	return d
}

func ptr[T any](v T) *T { return &v }

// The first event for a dispute creates its row (and says so); a redelivery
// is not a first event, an event that adds fields fills them in, and one
// that leaves fields out keeps what is stored.
func TestUpsertDisputeIdempotent(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	club := testutil.SeedClub(t, pool)
	attempt := seedAttempt(t, pool, club)
	due := time.Date(2026, 11, 20, 23, 59, 0, 0, time.UTC)

	created := payments.Dispute{ClubID: club, Provider: "stripe", ProviderDisputeID: "dp_test_1", AttemptID: attempt,
		ChargeRef: "ch_test_1", AmountOre: ptr(int64(45000)), Currency: "nok", Reason: "fraudulent",
		Status: "needs_response", EvidenceDueBy: &due}
	for i, wantFirst := range []bool{true, false, false} {
		first, err := payments.UpsertDispute(ctx, pool, created)
		if err != nil || first != wantFirst {
			t.Fatalf("delivery %d: first = %v, %v; want %v", i+1, first, err, wantFirst)
		}
	}
	got := readDispute(t, pool, "dp_test_1")
	if got.Status != "needs_response" || got.Reason != "fraudulent" || got.Currency != "NOK" ||
		got.AmountKr == nil || *got.AmountKr != 450 || got.Due == nil || !got.Due.Equal(due) || got.Attempt == nil || *got.Attempt != attempt {
		t.Errorf("stored = %+v", got)
	}

	// A bare update (no amount, reason, deadline or attempt) moves the status
	// and keeps the rest.
	if first, err := payments.UpsertDispute(ctx, pool, payments.Dispute{
		ClubID: club, Provider: "stripe", ProviderDisputeID: "dp_test_1", Status: "under_review"}); err != nil || first {
		t.Fatalf("update: first = %v, %v", first, err)
	}
	got = readDispute(t, pool, "dp_test_1")
	if got.Status != "under_review" || got.Reason != "fraudulent" || got.AmountKr == nil || *got.AmountKr != 450 ||
		got.Due == nil || got.Attempt == nil || got.Charge != "ch_test_1" {
		t.Errorf("after a bare update = %+v", got)
	}
}

// Events arrive out of order: an earlier status never replaces a later one,
// and a closed dispute is final.
func TestUpsertDisputeOutOfOrder(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	club := testutil.SeedClub(t, pool)
	other := testutil.SeedClub(t, pool)
	send := func(club, status string) {
		t.Helper()
		if _, err := payments.UpsertDispute(ctx, pool, payments.Dispute{
			ClubID: club, Provider: "stripe", ProviderDisputeID: "dp_test_2", AmountOre: ptr(int64(1000)), Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	steps := []struct{ send, want string }{
		{"under_review", "under_review"},
		{"needs_response", "under_review"},
		{"", "under_review"},
		{"lost", "lost"},
		{"under_review", "lost"},
		{"won", "lost"},
		{"lost", "lost"},
	}
	for _, s := range steps {
		send(club, s.send)
		if got := readDispute(t, pool, "dp_test_2").Status; got != s.want {
			t.Fatalf("after %q: status = %q, want %q", s.send, got, s.want)
		}
	}
	// Another club's event with the same dispute id writes nothing.
	send(other, "won")
	if got := readDispute(t, pool, "dp_test_2").Status; got != "lost" {
		t.Errorf("another club's event changed the dispute: %q", got)
	}

	if _, err := payments.UpsertDispute(ctx, pool, payments.Dispute{ClubID: club, Provider: "stripe"}); err == nil {
		t.Error("a dispute without an id was stored")
	}
}

// OpenDisputeForAttempt names the dispute that stops a refund. Without the
// provider's own answer, a chargeback awaiting a response or under review
// stops it, and so does a lost one (the provider refuses to refund it); an
// inquiry, a finished dispute and a status nobody knows do not. The
// provider's answer, when the event gave one, decides.
func TestOpenDisputeForAttempt(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	club := testutil.SeedClub(t, pool)
	attempt := seedAttempt(t, pool, club)
	elsewhere := seedAttempt(t, pool, club)
	due := time.Date(2026, 11, 20, 23, 59, 0, 0, time.UTC)

	open := func(attemptID string) *payments.OpenDispute {
		t.Helper()
		d, err := payments.OpenDisputeForAttempt(ctx, pool, club, attemptID)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if open(attempt) != nil {
		t.Fatal("a dispute was found for an attempt without one")
	}
	set := func(status string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE payment_disputes SET status = $1`, status); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := payments.UpsertDispute(ctx, pool, payments.Dispute{ClubID: club, Provider: "stripe",
		ProviderDisputeID: "dp_test_3", AttemptID: attempt, Status: "needs_response", EvidenceDueBy: &due}); err != nil {
		t.Fatal(err)
	}
	if d := open(attempt); d == nil || d.ProviderDisputeID != "dp_test_3" || d.Status != "needs_response" || d.EvidenceDueBy == nil {
		t.Fatalf("open dispute = %+v", d)
	}
	if open(elsewhere) != nil {
		t.Error("the dispute was found for another attempt")
	}
	for status, blocks := range map[string]bool{
		"needs_response": true, "under_review": true, "lost": true,
		"": false, "something_new": false, "won": false, "charge_refunded": false, "prevented": false,
		"warning_needs_response": false, "warning_under_review": false, "warning_closed": false,
	} {
		set(status)
		if got := open(attempt) != nil; got != blocks {
			t.Errorf("status %q: blocks refunds = %v, want %v", status, got, blocks)
		}
	}
	// Stripe's own answer wins over the status list, both ways.
	for _, tt := range []struct {
		status     string
		refundable bool
		blocks     bool
	}{
		{"warning_needs_response", false, true}, {"won", false, true},
		{"lost", true, false}, {"needs_response", true, false},
	} {
		set(tt.status)
		if _, err := pool.Exec(ctx, `UPDATE payment_disputes SET is_charge_refundable = $1`, tt.refundable); err != nil {
			t.Fatal(err)
		}
		if got := open(attempt) != nil; got != tt.blocks {
			t.Errorf("status %q, is_charge_refundable %v: blocks = %v, want %v", tt.status, tt.refundable, got, tt.blocks)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE payment_disputes SET is_charge_refundable = NULL`); err != nil {
		t.Fatal(err)
	}

	// Under a transaction, as a refund holding its attempt's lock reads it.
	set("needs_response")
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if d, err := payments.OpenDisputeForAttempt(ctx, tx, club, attempt); err != nil || d == nil {
		t.Errorf("under a transaction = %+v, %v", d, err)
	}
}

// The provider's is_charge_refundable is stored, kept when a later event
// leaves it out, and replaced when a later one gives it.
func TestUpsertDisputeKeepsRefundableAnswer(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	club := testutil.SeedClub(t, pool)
	send := func(status string, refundable *bool) {
		t.Helper()
		if _, err := payments.UpsertDispute(ctx, pool, payments.Dispute{ClubID: club, Provider: "stripe",
			ProviderDisputeID: "dp_test_4", Status: status, ChargeRefundable: refundable}); err != nil {
			t.Fatal(err)
		}
	}
	stored := func() *bool {
		t.Helper()
		var v *bool
		if err := pool.QueryRow(ctx, `SELECT is_charge_refundable FROM payment_disputes WHERE provider_dispute_id = 'dp_test_4'`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	send("needs_response", nil)
	if stored() != nil {
		t.Fatal("a refundable answer appeared from nowhere")
	}
	send("needs_response", ptr(false))
	send("under_review", nil)
	if v := stored(); v == nil || *v {
		t.Errorf("answer = %v after an event that left it out, want false", v)
	}
	send("under_review", ptr(true))
	if v := stored(); v == nil || !*v {
		t.Errorf("answer = %v, want true", v)
	}
}

// An inquiry (warning_*) escalates to a chargeback under the same dispute id,
// with a new deadline; an inquiry that closed can still escalate; a finished
// dispute is final. The treasurer is told once for each phase the dispute is
// open in, however often events repeat, and never for one stored already closed.
func TestUpsertDisputeEscalationAndNotices(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	club := testutil.SeedClub(t, pool)
	first := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	second := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	send := func(id, status string, due *time.Time) bool {
		t.Helper()
		notify, err := payments.UpsertDispute(ctx, pool, payments.Dispute{ClubID: club, Provider: "stripe",
			ProviderDisputeID: id, Status: status, EvidenceDueBy: due})
		if err != nil {
			t.Fatal(err)
		}
		return notify
	}

	steps := []struct {
		status     string
		due        *time.Time
		wantNotify bool
		wantStatus string
	}{
		{"warning_needs_response", &first, true, "warning_needs_response"},
		{"warning_needs_response", &first, false, "warning_needs_response"},
		{"warning_under_review", nil, false, "warning_under_review"},
		{"warning_closed", nil, false, "warning_closed"},
		// The bank escalates: same id, a chargeback with a new deadline.
		{"needs_response", &second, true, "needs_response"},
		{"needs_response", &second, false, "needs_response"},
		// A late inquiry event does not move it back.
		{"warning_under_review", nil, false, "needs_response"},
		{"under_review", nil, false, "under_review"},
		{"needs_response", nil, false, "under_review"},
		{"lost", nil, false, "lost"},
		{"needs_response", &first, false, "lost"},
		{"warning_needs_response", nil, false, "lost"},
		{"won", nil, false, "lost"},
	}
	for i, s := range steps {
		if got := send("dp_test_5", s.status, s.due); got != s.wantNotify {
			t.Errorf("step %d (%s): notify = %v, want %v", i+1, s.status, got, s.wantNotify)
		}
		if got := readDispute(t, pool, "dp_test_5"); got.Status != s.wantStatus {
			t.Fatalf("step %d (%s): status = %q, want %q", i+1, s.status, got.Status, s.wantStatus)
		}
	}
	if got := readDispute(t, pool, "dp_test_5"); got.Due == nil || !got.Due.Equal(second) {
		t.Errorf("deadline = %v, want the chargeback's %v", got.Due, second)
	}

	// Stored already closed, or with nothing said: never told.
	if send("dp_test_6", "won", nil) || send("dp_test_6", "under_review", nil) {
		t.Error("a dispute that arrived closed was told to the treasurer")
	}
	if send("dp_test_7", "", nil) || send("dp_test_7", "something_new", nil) {
		t.Error("a dispute with no known status was told to the treasurer")
	}
	// An out-of-order first event for a chargeback is told once, as is an
	// inquiry that goes straight to a chargeback.
	if !send("dp_test_8", "under_review", nil) || send("dp_test_8", "under_review", nil) {
		t.Error("a chargeback stored first should be told exactly once")
	}
}
