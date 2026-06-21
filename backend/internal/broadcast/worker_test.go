package broadcast

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

// fakeSender records calls and fails according to a per-email policy.
type fakeSender struct {
	mu      sync.Mutex
	calls   []string // emails, in order
	msgs    []OutgoingMessage
	failFor map[string]int // email → number of leading attempts to fail
	failAll bool
	sentAt  []time.Time
}

func newFakeSender() *fakeSender {
	return &fakeSender{failFor: map[string]int{}}
}

func (f *fakeSender) SendBroadcast(_ context.Context, _ string, msg OutgoingMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, msg.To)
	f.msgs = append(f.msgs, msg)
	f.sentAt = append(f.sentAt, time.Now())
	if f.failAll {
		return fmt.Errorf("permanent failure")
	}
	if n := f.failFor[msg.To]; n > 0 {
		f.failFor[msg.To] = n - 1
		return fmt.Errorf("transient failure")
	}
	return nil
}

func (f *fakeSender) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func workerStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	pool := testutil.SetupTestDB(t)
	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"board"})
	return NewStore(pool), clubID, userID
}

func enqueueN(t *testing.T, s *Store, clubID, sentBy string, emails ...string) string {
	t.Helper()
	recs := make([]Recipient, 0, len(emails))
	for _, e := range emails {
		recs = append(recs, Recipient{Email: e})
	}
	id, err := s.Enqueue(context.Background(), New{
		ClubID: clubID, SentBy: sentBy, SourceAddress: "kasserar@x.no",
		Subject: "Subject", BodyText: "Body", Recipients: "test",
	}, recs)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return id
}

func TestWorkerDrainsBatch(t *testing.T) {
	s, clubID, userID := workerStore(t)
	ctx := context.Background()
	id := enqueueN(t, s, clubID, userID, "a@x.no", "b@x.no", "c@x.no")

	sender := newFakeSender()
	w := NewWorker(s, sender, 0, zerolog.Nop())
	w.drain(ctx)

	if sender.callCount() != 3 {
		t.Errorf("sends = %d, want 3", sender.callCount())
	}
	d, err := s.Get(ctx, clubID, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d.Sent != 3 || d.Pending != 0 || d.Failed != 0 {
		t.Errorf("counts = sent%d/pending%d/failed%d, want 3/0/0", d.Sent, d.Pending, d.Failed)
	}
	if d.Status != BroadcastComplete {
		t.Errorf("status = %q, want complete", d.Status)
	}
}

func TestWorkerRetriesTransientThenSucceeds(t *testing.T) {
	s, clubID, userID := workerStore(t)
	ctx := context.Background()
	id := enqueueN(t, s, clubID, userID, "flaky@x.no")

	sender := newFakeSender()
	sender.failFor["flaky@x.no"] = 1 // fail once, then succeed
	w := NewWorker(s, sender, 0, zerolog.Nop())
	w.MaxAttempts = 3

	w.drain(ctx) // attempt 1 fails → back to pending, attempt 2 succeeds

	d, err := s.Get(ctx, clubID, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d.Sent != 1 || d.Failed != 0 {
		t.Errorf("counts = sent%d/failed%d, want 1/0", d.Sent, d.Failed)
	}
	if d.Deliveries[0].Attempts != 2 {
		t.Errorf("attempts = %d, want 2 (one retry)", d.Deliveries[0].Attempts)
	}
	if sender.callCount() != 2 {
		t.Errorf("sends = %d, want 2", sender.callCount())
	}
}

func TestWorkerTerminalFailAfterCap(t *testing.T) {
	s, clubID, userID := workerStore(t)
	ctx := context.Background()
	id := enqueueN(t, s, clubID, userID, "dead@x.no")

	sender := newFakeSender()
	sender.failAll = true
	w := NewWorker(s, sender, 0, zerolog.Nop())
	w.MaxAttempts = 3

	w.drain(ctx)

	d, err := s.Get(ctx, clubID, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d.Failed != 1 || d.Sent != 0 || d.Pending != 0 {
		t.Errorf("counts = sent%d/pending%d/failed%d, want 0/0/1", d.Sent, d.Pending, d.Failed)
	}
	if d.Deliveries[0].Attempts != 3 {
		t.Errorf("attempts = %d, want 3 (cap)", d.Deliveries[0].Attempts)
	}
	if sender.callCount() != 3 {
		t.Errorf("sends = %d, want 3 (cap)", sender.callCount())
	}
	if d.Status != BroadcastComplete {
		t.Errorf("status = %q, want complete (all resolved, even if failed)", d.Status)
	}
}

func TestWorkerRespectsThrottle(t *testing.T) {
	s, clubID, userID := workerStore(t)
	ctx := context.Background()
	enqueueN(t, s, clubID, userID, "a@x.no", "b@x.no", "c@x.no")

	throttle := 40 * time.Millisecond
	sender := newFakeSender()
	w := NewWorker(s, sender, throttle, zerolog.Nop())

	start := time.Now()
	w.drain(ctx)
	elapsed := time.Since(start)

	// Three sends, each followed by one throttle sleep → at least ~3×.
	// Use a generous lower bound (2×) to stay robust on slow CI.
	if elapsed < 2*throttle {
		t.Errorf("elapsed %v, want >= %v (throttle respected)", elapsed, 2*throttle)
	}
}

func TestWorkerForwardsAttachments(t *testing.T) {
	s, clubID, userID := workerStore(t)
	ctx := context.Background()
	parts := []map[string]any{
		{"blobId": "blob-1", "type": "application/pdf", "name": "agenda.pdf", "disposition": "attachment"},
		{"blobId": "blob-2", "type": "image/png", "name": "logo.png", "disposition": "inline", "cid": "logo@x"},
	}
	id, err := s.Enqueue(ctx, New{
		ClubID: clubID, SentBy: userID, SourceAddress: "kasserar@x.no",
		Subject: "S", BodyText: "B", Recipients: "test", Attachments: parts,
	}, []Recipient{{Email: "a@x.no"}})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Claim round-trips the attachments out of JSONB.
	claimed, err := s.ClaimPending(ctx, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimPending: %v (n=%d)", err, len(claimed))
	}
	if len(claimed[0].Attachments) != 2 {
		t.Fatalf("claimed attachments = %d, want 2", len(claimed[0].Attachments))
	}
	if claimed[0].Attachments[0]["blobId"] != "blob-1" {
		t.Errorf("first blobId = %v, want blob-1", claimed[0].Attachments[0]["blobId"])
	}

	// Re-queue and let the worker drain so we assert the sender receives them.
	if _, err := s.ResetOrphanedSending(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	sender := newFakeSender()
	w := NewWorker(s, sender, 0, zerolog.Nop())
	w.drain(ctx)

	if len(sender.msgs) != 1 {
		t.Fatalf("sends = %d, want 1", len(sender.msgs))
	}
	got := sender.msgs[0].AttachBodyParts
	if len(got) != 2 {
		t.Fatalf("forwarded attachments = %d, want 2", len(got))
	}
	if got[1]["cid"] != "logo@x" || got[1]["disposition"] != "inline" {
		t.Errorf("inline part not forwarded intact: %+v", got[1])
	}
	_ = id
}

func TestWorkerBootSweepReclaimsOrphans(t *testing.T) {
	s, clubID, userID := workerStore(t)
	ctx := context.Background()
	id := enqueueN(t, s, clubID, userID, "a@x.no", "b@x.no")

	// Simulate a crash mid-flight: claim both (→ 'sending') but never mark.
	if _, err := s.ClaimPending(ctx, 10); err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}

	// A fresh worker draining without a boot sweep sees no 'pending' rows.
	sender := newFakeSender()
	w := NewWorker(s, sender, 0, zerolog.Nop())
	w.drain(ctx)
	if sender.callCount() != 0 {
		t.Fatalf("pre-sweep sends = %d, want 0 (rows stuck in sending)", sender.callCount())
	}

	// Boot sweep returns orphaned 'sending' rows to 'pending'; drain sends them.
	if _, err := s.ResetOrphanedSending(ctx); err != nil {
		t.Fatalf("ResetOrphanedSending: %v", err)
	}
	w.drain(ctx)
	if sender.callCount() != 2 {
		t.Errorf("post-sweep sends = %d, want 2", sender.callCount())
	}
	d, _ := s.Get(ctx, clubID, id)
	if d.Sent != 2 || d.Status != BroadcastComplete {
		t.Errorf("after sweep: sent%d status%q, want 2/complete", d.Sent, d.Status)
	}
}
