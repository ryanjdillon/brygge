package broadcast_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

// seedRecipients makes n recipients, m of them mapped to seeded members.
func ptr(s string) *string { return &s }

func newStore(t *testing.T) (*broadcast.Store, *pgxpool.Pool, string, string) {
	t.Helper()
	pool := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"board"})
	return broadcast.NewStore(pool), pool, clubID, userID
}

func enqueueSample(t *testing.T, s *broadcast.Store, clubID, sentBy string, recips []broadcast.Recipient) string {
	t.Helper()
	id, err := s.Enqueue(context.Background(), broadcast.New{
		ClubID:        clubID,
		SentBy:        sentBy,
		SourceAddress: "kasserar@klokkarvikbaatlag.no",
		Subject:       "AGM på lørdag",
		BodyText:      "Hei alle",
		BodyHTML:      "<p>Hei alle</p>",
		Recipients:    "Members",
	}, recips)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return id
}

func TestEnqueueWritesParentAndDeliveries(t *testing.T) {
	s, pool, clubID, userID := newStore(t)
	ctx := context.Background()

	id := enqueueSample(t, s, clubID, userID, []broadcast.Recipient{
		{UserID: ptr(userID), Email: "a@example.com"},
		{UserID: nil, Email: "b@example.com"},
	})
	if id == "" {
		t.Fatal("expected broadcast id")
	}

	var bStatus, bSubject, bHTML string
	if err := pool.QueryRow(ctx,
		`SELECT status, subject, body_html FROM broadcasts WHERE id = $1`, id,
	).Scan(&bStatus, &bSubject, &bHTML); err != nil {
		t.Fatalf("read broadcast: %v", err)
	}
	if bStatus != broadcast.BroadcastPending {
		t.Errorf("parent status = %q, want pending", bStatus)
	}
	if bSubject != "AGM på lørdag" || bHTML != "<p>Hei alle</p>" {
		t.Errorf("parent fields not persisted: %q / %q", bSubject, bHTML)
	}

	var n, pending int
	if err := pool.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE status = 'pending') FROM broadcast_deliveries WHERE broadcast_id = $1`, id,
	).Scan(&n, &pending); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if n != 2 || pending != 2 {
		t.Errorf("deliveries = %d (pending %d), want 2/2", n, pending)
	}
}

func TestClaimPendingFlipsToSendingAndJoinsParent(t *testing.T) {
	s, pool, clubID, userID := newStore(t)
	ctx := context.Background()
	id := enqueueSample(t, s, clubID, userID, []broadcast.Recipient{
		{Email: "a@example.com"}, {Email: "b@example.com"},
	})

	claimed, err := s.ClaimPending(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d, want 2", len(claimed))
	}
	for _, c := range claimed {
		if c.BroadcastID != id {
			t.Errorf("broadcast id = %q, want %q", c.BroadcastID, id)
		}
		if c.Subject != "AGM på lørdag" || c.SourceAddress != "kasserar@klokkarvikbaatlag.no" {
			t.Errorf("parent join missing: %q / %q", c.Subject, c.SourceAddress)
		}
	}

	// A second claim returns nothing — all rows are 'sending' now.
	again, err := s.ClaimPending(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimPending (2): %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second claim returned %d, want 0", len(again))
	}

	var sending int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM broadcast_deliveries WHERE broadcast_id = $1 AND status = 'sending'`, id,
	).Scan(&sending); err != nil {
		t.Fatalf("count sending: %v", err)
	}
	if sending != 2 {
		t.Errorf("sending = %d, want 2", sending)
	}
}

func TestClaimPendingRespectsLimit(t *testing.T) {
	s, _, clubID, userID := newStore(t)
	ctx := context.Background()
	enqueueSample(t, s, clubID, userID, []broadcast.Recipient{
		{Email: "a@example.com"}, {Email: "b@example.com"}, {Email: "c@example.com"},
	})

	claimed, err := s.ClaimPending(ctx, 2)
	if err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}
	if len(claimed) != 2 {
		t.Errorf("claimed %d, want 2 (limit)", len(claimed))
	}
}

func TestMarkSentAndFailed(t *testing.T) {
	s, pool, clubID, userID := newStore(t)
	ctx := context.Background()
	enqueueSample(t, s, clubID, userID, []broadcast.Recipient{
		{Email: "ok@example.com"}, {Email: "bad@example.com"},
	})
	claimed, _ := s.ClaimPending(ctx, 10)

	for _, c := range claimed {
		if c.Email == "ok@example.com" {
			if err := s.Mark(ctx, c.DeliveryID, broadcast.DeliverySent, ""); err != nil {
				t.Fatalf("Mark sent: %v", err)
			}
		} else {
			if err := s.Mark(ctx, c.DeliveryID, broadcast.DeliveryFailed, "550 rejected"); err != nil {
				t.Fatalf("Mark failed: %v", err)
			}
		}
	}

	type row struct {
		status   string
		attempts int
		errMsg   string
		sentSet  bool
	}
	got := map[string]row{}
	rows, err := pool.Query(ctx,
		`SELECT email, status, attempts, error, sent_at IS NOT NULL FROM broadcast_deliveries`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var email string
		var r row
		if err := rows.Scan(&email, &r.status, &r.attempts, &r.errMsg, &r.sentSet); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[email] = r
	}

	if ok := got["ok@example.com"]; ok.status != "sent" || ok.attempts != 1 || !ok.sentSet {
		t.Errorf("ok row = %+v, want sent/1/sent_at set", ok)
	}
	if bad := got["bad@example.com"]; bad.status != "failed" || bad.attempts != 1 || bad.errMsg != "550 rejected" || bad.sentSet {
		t.Errorf("bad row = %+v, want failed/1/error/no sent_at", bad)
	}
}

func TestFinalizeIfDone(t *testing.T) {
	s, pool, clubID, userID := newStore(t)
	ctx := context.Background()
	id := enqueueSample(t, s, clubID, userID, []broadcast.Recipient{
		{Email: "a@example.com"}, {Email: "b@example.com"},
	})
	claimed, _ := s.ClaimPending(ctx, 10)

	// Mark only the first; one still 'sending' → not complete.
	_ = s.Mark(ctx, claimed[0].DeliveryID, broadcast.DeliverySent, "")
	if err := s.FinalizeIfDone(ctx, id); err != nil {
		t.Fatalf("FinalizeIfDone: %v", err)
	}
	if got := broadcastStatus(t, pool, id); got != broadcast.BroadcastPending {
		t.Errorf("status = %q, want pending (one still sending)", got)
	}

	// Finish the second → complete.
	_ = s.Mark(ctx, claimed[1].DeliveryID, broadcast.DeliveryFailed, "oops")
	if err := s.FinalizeIfDone(ctx, id); err != nil {
		t.Fatalf("FinalizeIfDone: %v", err)
	}
	if got := broadcastStatus(t, pool, id); got != broadcast.BroadcastComplete {
		t.Errorf("status = %q, want complete", got)
	}
}

func TestResetOrphanedSending(t *testing.T) {
	s, pool, clubID, userID := newStore(t)
	ctx := context.Background()
	id := enqueueSample(t, s, clubID, userID, []broadcast.Recipient{
		{Email: "a@example.com"}, {Email: "b@example.com"},
	})
	if _, err := s.ClaimPending(ctx, 10); err != nil { // both → sending
		t.Fatalf("ClaimPending: %v", err)
	}

	n, err := s.ResetOrphanedSending(ctx)
	if err != nil {
		t.Fatalf("ResetOrphanedSending: %v", err)
	}
	if n != 2 {
		t.Errorf("reset %d, want 2", n)
	}
	var pending int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM broadcast_deliveries WHERE broadcast_id = $1 AND status = 'pending'`, id,
	).Scan(&pending); err != nil {
		t.Fatalf("count: %v", err)
	}
	if pending != 2 {
		t.Errorf("pending after reset = %d, want 2", pending)
	}
}

func TestListAggregatesCounts(t *testing.T) {
	s, _, clubID, userID := newStore(t)
	ctx := context.Background()
	id := enqueueSample(t, s, clubID, userID, []broadcast.Recipient{
		{Email: "a@example.com"}, {Email: "b@example.com"}, {Email: "c@example.com"},
	})
	claimed, _ := s.ClaimPending(ctx, 10)
	_ = s.Mark(ctx, claimed[0].DeliveryID, broadcast.DeliverySent, "")
	_ = s.Mark(ctx, claimed[1].DeliveryID, broadcast.DeliveryFailed, "x")
	// third stays 'sending' (counts as pending bucket)

	list, err := s.List(ctx, clubID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
	got := list[0]
	if got.ID != id || got.Total != 3 || got.Sent != 1 || got.Failed != 1 || got.Pending != 1 {
		t.Errorf("summary = %+v, want total3/sent1/failed1/pending1", got)
	}
}

func TestGetDetailAndClubScoping(t *testing.T) {
	s, pool, clubID, userID := newStore(t)
	ctx := context.Background()
	id := enqueueSample(t, s, clubID, userID, []broadcast.Recipient{
		{Email: "a@example.com"}, {Email: "b@example.com"},
	})

	d, err := s.Get(ctx, clubID, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d.BodyText != "Hei alle" || len(d.Deliveries) != 2 {
		t.Errorf("detail = %+v, want body + 2 deliveries", d)
	}

	// A different club must not see it.
	otherClub := testutil.SeedClub(t, pool)
	if _, err := s.Get(ctx, otherClub, id); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("cross-club Get err = %v, want ErrNoRows", err)
	}
}

func TestRequeueFailed(t *testing.T) {
	s, pool, clubID, userID := newStore(t)
	ctx := context.Background()
	id := enqueueSample(t, s, clubID, userID, []broadcast.Recipient{
		{Email: "ok@example.com"}, {Email: "bad@example.com"},
	})
	claimed, _ := s.ClaimPending(ctx, 10)
	for _, c := range claimed {
		if c.Email == "ok@example.com" {
			_ = s.Mark(ctx, c.DeliveryID, broadcast.DeliverySent, "")
		} else {
			_ = s.Mark(ctx, c.DeliveryID, broadcast.DeliveryFailed, "550")
		}
	}
	_ = s.FinalizeIfDone(ctx, id)

	n, err := s.RequeueFailed(ctx, clubID, id)
	if err != nil {
		t.Fatalf("RequeueFailed: %v", err)
	}
	if n != 1 {
		t.Errorf("requeued %d, want 1 (only failed)", n)
	}

	// The sent row is untouched; the failed row is pending again; parent reset.
	var sent, pending int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE status='sent'), count(*) FILTER (WHERE status='pending')
		 FROM broadcast_deliveries WHERE broadcast_id = $1`, id,
	).Scan(&sent, &pending); err != nil {
		t.Fatalf("count: %v", err)
	}
	if sent != 1 || pending != 1 {
		t.Errorf("after requeue sent=%d pending=%d, want 1/1", sent, pending)
	}
	if got := broadcastStatus(t, pool, id); got != broadcast.BroadcastPending {
		t.Errorf("parent status = %q, want pending after requeue", got)
	}

	// Cross-club requeue is a no-op error.
	otherClub := testutil.SeedClub(t, pool)
	if _, err := s.RequeueFailed(ctx, otherClub, id); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("cross-club RequeueFailed err = %v, want ErrNoRows", err)
	}
}

func broadcastStatus(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM broadcasts WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return status
}
