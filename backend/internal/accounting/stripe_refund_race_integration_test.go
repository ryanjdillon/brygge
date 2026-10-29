package accounting

import (
	"context"
	"testing"
	"time"
)

// The refund webhook may insert a Dashboard refund's row while the booking
// pass is about to: the pass's insert yields to it and reads it again, rather
// than failing on the unique index and aborting the booking.
func TestMatchPaymentRefundYieldsToConcurrentInsert(t *testing.T) {
	f := newStripeFixture(t)
	ctx := context.Background()
	attemptID, _ := f.attempt(t, 450, "NOK")

	webhook, err := f.club.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = webhook.Rollback(ctx) }()
	if _, err := webhook.Exec(ctx,
		`INSERT INTO payment_refunds (club_id, attempt_id, provider, amount, provider_refund_id, status, reason)
		 VALUES ($1, $2, 'stripe', 90, 're_race', 'succeeded', 'Stripe Dashboard')`, f.club.ID, attemptID); err != nil {
		t.Fatal(err)
	}

	pass, err := f.club.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pass.Rollback(ctx) }()
	row := &stripeTxnRow{sourceID: "re_race", chargeID: "ch_race", amountOre: -9000}
	type result struct {
		m   paymentRefundMatch
		err error
	}
	done := make(chan result, 1)
	go func() {
		m, err := matchPaymentRefundTx(ctx, pass, f.club.ID, row, &stripeOrigin{attemptID: &attemptID})
		done <- result{m, err}
	}()

	// The pass's insert waits on the webhook's uncommitted row.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := f.club.Pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND datname = current_database()`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the pass never waited on the webhook's row")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := webhook.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	res := <-done
	if res.err != nil || res.m.id == nil {
		t.Fatalf("match = %+v, err = %v, want the webhook's row", res.m, res.err)
	}
	if err := pass.Commit(ctx); err != nil {
		t.Fatalf("the pass's transaction did not survive the race: %v", err)
	}
	var n int
	if err := f.club.Pool.QueryRow(ctx,
		`SELECT count(*) FROM payment_refunds WHERE club_id = $1 AND provider_refund_id = 're_race'`, f.club.ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("rows for the refund = %d (%v), want 1", n, err)
	}
}
