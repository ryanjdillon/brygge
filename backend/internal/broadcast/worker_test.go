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

// newBroadcastWorker builds a worker with the broadcast Sender registered,
// wrapping the fake MessageSender so the existing broadcast tests exercise
// the full claim → load-content → send path through the registry.
func newBroadcastWorker(s *Store, sender MessageSender, throttle time.Duration) *Worker {
	w := NewWorker(s, throttle, zerolog.Nop())
	w.RegisterSender(KindBroadcast, NewBroadcastSender(s, sender))
	return w
}

// fakeInvoiceSender is a broadcast.Sender for invoice-reminder deliveries.
type fakeInvoiceSender struct {
	mu      sync.Mutex
	calls   []string       // invoice ids, in order
	failFor map[string]int // invoice id → leading attempts to fail
}

func newFakeInvoiceSender() *fakeInvoiceSender {
	return &fakeInvoiceSender{failFor: map[string]int{}}
}

func (f *fakeInvoiceSender) Send(_ context.Context, c Claimed) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := ""
	if c.InvoiceID != nil {
		id = *c.InvoiceID
	}
	f.calls = append(f.calls, id)
	if n := f.failFor[id]; n > 0 {
		f.failFor[id] = n - 1
		return fmt.Errorf("transient failure")
	}
	return nil
}

func (f *fakeInvoiceSender) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func seedInvoiceW(t *testing.T, s *Store, clubID, kid string) string {
	t.Helper()
	var id string
	if err := s.db.QueryRow(context.Background(),
		`INSERT INTO invoices (club_id, kid_number, due_date, total_amount, sent_at)
		 VALUES ($1, $2, CURRENT_DATE, 100.00, now()) RETURNING id`,
		clubID, kid,
	).Scan(&id); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	return id
}

func invoiceDeliveryStatus(t *testing.T, s *Store, invoiceID string) string {
	t.Helper()
	var st string
	if err := s.db.QueryRow(context.Background(),
		`SELECT status FROM deliveries WHERE invoice_id = $1 AND kind = 'invoice_reminder'`,
		invoiceID,
	).Scan(&st); err != nil {
		t.Fatalf("read delivery status: %v", err)
	}
	return st
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
	w := newBroadcastWorker(s, sender, 0)
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
	w := newBroadcastWorker(s, sender, 0)
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
	w := newBroadcastWorker(s, sender, 0)
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
	w := newBroadcastWorker(s, sender, throttle)

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
	if _, err := s.Enqueue(ctx, New{
		ClubID: clubID, SentBy: userID, SourceAddress: "kasserar@x.no",
		Subject: "S", BodyText: "B", Recipients: "test", Attachments: parts,
	}, []Recipient{{Email: "a@x.no"}}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// The broadcast Sender loads attachments from the parent row (JSONB
	// round-trip) and forwards them to the message backend intact.
	sender := newFakeSender()
	w := newBroadcastWorker(s, sender, 0)
	w.drain(ctx)

	if len(sender.msgs) != 1 {
		t.Fatalf("sends = %d, want 1", len(sender.msgs))
	}
	got := sender.msgs[0].AttachBodyParts
	if len(got) != 2 {
		t.Fatalf("forwarded attachments = %d, want 2", len(got))
	}
	if got[0]["blobId"] != "blob-1" {
		t.Errorf("first blobId = %v, want blob-1", got[0]["blobId"])
	}
	if got[1]["cid"] != "logo@x" || got[1]["disposition"] != "inline" {
		t.Errorf("inline part not forwarded intact: %+v", got[1])
	}
}

func TestWorkerDrainsInvoiceReminder(t *testing.T) {
	s, clubID, _ := workerStore(t)
	ctx := context.Background()
	inv := seedInvoiceW(t, s, clubID, "000000010012")
	if enq, _, err := s.EnqueueInvoiceReminders(ctx, clubID, []InvoiceDelivery{
		{InvoiceID: inv, Email: "a@x.no", Payload: []byte(`{}`)},
	}); err != nil || enq != 1 {
		t.Fatalf("enqueue reminder: %v (n=%d)", err, enq)
	}

	sender := newFakeInvoiceSender()
	w := NewWorker(s, 0, zerolog.Nop())
	w.RegisterSender(KindInvoiceReminder, sender)
	w.drain(ctx)

	if sender.callCount() != 1 {
		t.Errorf("sends = %d, want 1", sender.callCount())
	}
	if st := invoiceDeliveryStatus(t, s, inv); st != DeliverySent {
		t.Errorf("status = %q, want sent", st)
	}
}

func TestWorkerRetriesInvoiceReminderThenSucceeds(t *testing.T) {
	s, clubID, _ := workerStore(t)
	ctx := context.Background()
	inv := seedInvoiceW(t, s, clubID, "000000020010")
	if _, _, err := s.EnqueueInvoiceReminders(ctx, clubID, []InvoiceDelivery{
		{InvoiceID: inv, Email: "flaky@x.no", Payload: []byte(`{}`)},
	}); err != nil {
		t.Fatalf("enqueue reminder: %v", err)
	}

	sender := newFakeInvoiceSender()
	sender.failFor[inv] = 1 // fail once, then succeed
	w := NewWorker(s, 0, zerolog.Nop())
	w.MaxAttempts = 3
	w.RegisterSender(KindInvoiceReminder, sender)
	w.drain(ctx)

	if sender.callCount() != 2 {
		t.Errorf("sends = %d, want 2 (one retry)", sender.callCount())
	}
	if st := invoiceDeliveryStatus(t, s, inv); st != DeliverySent {
		t.Errorf("status = %q, want sent after retry", st)
	}
}

func TestWorkerInvoiceReminderTerminalFailAfterCap(t *testing.T) {
	s, clubID, _ := workerStore(t)
	ctx := context.Background()
	inv := seedInvoiceW(t, s, clubID, "000000030018")
	if _, _, err := s.EnqueueInvoiceReminders(ctx, clubID, []InvoiceDelivery{
		{InvoiceID: inv, Email: "dead@x.no", Payload: []byte(`{}`)},
	}); err != nil {
		t.Fatalf("enqueue reminder: %v", err)
	}

	sender := newFakeInvoiceSender()
	sender.failFor[inv] = 99 // never succeeds
	w := NewWorker(s, 0, zerolog.Nop())
	w.MaxAttempts = 3
	w.RegisterSender(KindInvoiceReminder, sender)
	w.drain(ctx)

	if sender.callCount() != 3 {
		t.Errorf("sends = %d, want 3 (cap)", sender.callCount())
	}
	if st := invoiceDeliveryStatus(t, s, inv); st != DeliveryFailed {
		t.Errorf("status = %q, want failed at cap", st)
	}
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
	w := newBroadcastWorker(s, sender, 0)
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
