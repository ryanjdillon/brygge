package sesevents_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brygge-klubb/brygge/internal/sesevents"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

const fixtureSendID = "3f1c2a9e-7b4d-4e2a-9c61-0d5b8e7f1a23"

func parseFixture(t *testing.T, name string) []sesevents.Record {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	recs, err := sesevents.Parse(b)
	if err != nil {
		t.Fatalf("Parse %s: %v", name, err)
	}
	return recs
}

func seedSend(t *testing.T, pool *pgxpool.Pool, status string) {
	t.Helper()
	clubID := testutil.SeedClub(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO email_sends (id, club_id, kind, recipient, recipient_domain, status)
		VALUES ($1, $2, 'invoice', 'member@example.net', 'example.net', $3)`,
		fixtureSendID, clubID, status); err != nil {
		t.Fatalf("seed send: %v", err)
	}
}

func sendState(t *testing.T, pool *pgxpool.Pool) (status, messageID string) {
	t.Helper()
	var mid *string
	if err := pool.QueryRow(context.Background(),
		`SELECT status, ses_message_id FROM email_sends WHERE id = $1`, fixtureSendID,
	).Scan(&status, &mid); err != nil {
		t.Fatal(err)
	}
	if mid != nil {
		messageID = *mid
	}
	return status, messageID
}

func countEvents(t *testing.T, pool *pgxpool.Pool) (all, linked int) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*), count(send_id) FROM mail_delivery_events`,
	).Scan(&all, &linked); err != nil {
		t.Fatal(err)
	}
	return all, linked
}

func TestStoreApplyIdempotentAndRanked(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	seedSend(t, pool, "sending")
	s := sesevents.NewStore(pool)

	// Delivery first, then the Send it followed: the late Send must not
	// move the send back to queued.
	got, err := s.Apply(ctx, parseFixture(t, "delivery.json"))
	if err != nil || len(got) != 1 {
		t.Fatalf("Apply delivery: %v, %d new", err, len(got))
	}
	if _, err := s.Apply(ctx, parseFixture(t, "send.json")); err != nil {
		t.Fatal(err)
	}
	status, mid := sendState(t, pool)
	if status != sesevents.StatusAccepted {
		t.Fatalf("status = %q, want accepted", status)
	}
	if mid == "" {
		t.Fatal("ses_message_id not set from the event")
	}

	// SQS redelivery: the same event again is dropped.
	got, err = s.Apply(ctx, parseFixture(t, "delivery.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("redelivered event reported as new: %d", len(got))
	}
	if all, linked := countEvents(t, pool); all != 2 || linked != 2 {
		t.Fatalf("events = %d (linked %d), want 2 (2)", all, linked)
	}

	if _, err := s.Apply(ctx, parseFixture(t, "complaint.json")); err != nil {
		t.Fatal(err)
	}
	if status, _ := sendState(t, pool); status != sesevents.StatusComplaint {
		t.Fatalf("status = %q, want complaint", status)
	}
}

func TestStoreApplyUnknownSendStoresUnlinkedEvent(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := sesevents.NewStore(pool)

	// No email_sends row exists: the event is kept with a NULL send_id.
	got, err := s.Apply(context.Background(), parseFixture(t, "bounce_multi.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("new = %d, want 2", len(got))
	}
	if all, linked := countEvents(t, pool); all != 2 || linked != 0 {
		t.Fatalf("events = %d (linked %d), want 2 (0)", all, linked)
	}
	var domain string
	if err := pool.QueryRow(context.Background(),
		`SELECT recipient_domain FROM mail_delivery_events WHERE recipient = 'Second@Example.ORG'`,
	).Scan(&domain); err != nil {
		t.Fatal(err)
	}
	if domain != "example.org" {
		t.Fatalf("recipient_domain = %q", domain)
	}
}

func TestStoreApplyNoRecipientDeduplicates(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := sesevents.NewStore(pool)
	body := []byte(`{"eventType":"Send","mail":{"messageId":"no-recipient","timestamp":"2026-11-03T10:00:00Z"},"send":{}}`)
	for i := 0; i < 2; i++ {
		recs, err := sesevents.Parse(body)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Apply(context.Background(), recs); err != nil {
			t.Fatal(err)
		}
	}
	if all, _ := countEvents(t, pool); all != 1 {
		t.Fatalf("events = %d, want 1 (NULL recipient must dedupe)", all)
	}
}

func TestStorePurgeExpired(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	seedSend(t, pool, "sending")
	s := sesevents.NewStore(pool)
	if _, err := s.Apply(ctx, parseFixture(t, "delivery.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO mail_delivery_events (ses_message_id, event_type, event_at, raw, received_at)
		VALUES ('old', 'send', now() - interval '14 months', '{}', now() - interval '14 months')`); err != nil {
		t.Fatal(err)
	}

	sends, events, err := s.PurgeExpired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sends != 0 || events != 1 {
		t.Fatalf("purged sends=%d events=%d, want 0 and 1", sends, events)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE email_sends SET created_at = now() - interval '14 months'`); err != nil {
		t.Fatal(err)
	}
	sends, _, err = s.PurgeExpired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sends != 1 {
		t.Fatalf("purged sends=%d, want 1", sends)
	}
	if all, _ := countEvents(t, pool); all != 0 {
		t.Fatalf("events left = %d, want 0 (cascade from the purged send)", all)
	}
}
