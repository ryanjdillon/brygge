package accounting

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/testutil"
)

// Erasing a member deletes the mail log that names them, by user id or by
// their address in the club, plus unmatched SES events for that address. Other
// members' mail and another club's mail to the same address stay, and a
// member the purge skips keeps theirs.
func TestIntegration_PurgeDeletesMemberMailLog(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	otherClub := testutil.SeedClub(t, pool)
	svc := NewService(pool, nil, zerolog.Nop())

	member, email := testutil.SeedUser(t, pool, clubID, []string{"member"})
	bystander, bystanderEmail := testutil.SeedUser(t, pool, clubID, []string{"member"})

	send := func(club string, user any, recipient string) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO email_sends (club_id, user_id, kind, recipient, recipient_domain, status)
			 VALUES ($1, $2, 'invoice', $3, split_part($3, '@', 2), 'queued') RETURNING id`,
			club, user, recipient,
		).Scan(&id); err != nil {
			t.Fatalf("seed send: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO mail_delivery_events (send_id, ses_message_id, event_type, event_at, recipient, raw)
			 VALUES ($1, $3, 'delivery', now(), $2, '{}')`,
			id, recipient, "synthetic-"+id,
		); err != nil {
			t.Fatalf("seed event: %v", err)
		}
		return id
	}
	unmatched := func(recipient string) {
		if _, err := pool.Exec(ctx,
			`INSERT INTO mail_delivery_events (ses_message_id, event_type, event_at, recipient, raw)
			 VALUES ('synthetic-unmatched-' || $1, 'bounce', now(), $1, '{}')`,
			recipient,
		); err != nil {
			t.Fatalf("seed unmatched event: %v", err)
		}
	}

	byUser := send(clubID, member, email)
	byAddress := send(clubID, nil, email)
	keptBystander := send(clubID, bystander, bystanderEmail)
	keptOtherClub := send(otherClub, nil, email)
	unmatched(email)
	unmatched(bystanderEmail)

	count := func(q string, args ...any) int {
		var n int
		if err := pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}

	// Active, so the purge skips the member and the deletes roll back.
	if done, err := svc.purgeOneMember(ctx, member, clubID); err != nil || done {
		t.Fatalf("purgeOneMember on an active member = (%v, %v), want (false, nil)", done, err)
	}
	if n := count(`SELECT count(*) FROM email_sends WHERE id = ANY($1)`, []string{byUser, byAddress}); n != 2 {
		t.Fatalf("skipped member's sends = %d, want 2 kept", n)
	}

	if _, err := pool.Exec(ctx, `UPDATE users SET deactivated_at = now() WHERE id = $1`, member); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if done, err := svc.purgeOneMember(ctx, member, clubID); err != nil || !done {
		t.Fatalf("purgeOneMember = (%v, %v), want (true, nil)", done, err)
	}

	if n := count(`SELECT count(*) FROM email_sends WHERE id = ANY($1)`, []string{byUser, byAddress}); n != 0 {
		t.Errorf("member's sends left = %d, want 0", n)
	}
	if n := count(`SELECT count(*) FROM mail_delivery_events WHERE send_id = ANY($1)`, []string{byUser, byAddress}); n != 0 {
		t.Errorf("member's send events left = %d, want 0 (cascade)", n)
	}
	if n := count(`SELECT count(*) FROM mail_delivery_events WHERE send_id IS NULL AND recipient = $1`, email); n != 0 {
		t.Errorf("member's unmatched events left = %d, want 0", n)
	}
	if n := count(`SELECT count(*) FROM email_sends WHERE id = ANY($1)`, []string{keptBystander, keptOtherClub}); n != 2 {
		t.Errorf("other sends left = %d, want 2", n)
	}
	if n := count(`SELECT count(*) FROM mail_delivery_events WHERE send_id IS NULL AND recipient = $1`, bystanderEmail); n != 1 {
		t.Errorf("bystander's unmatched events left = %d, want 1", n)
	}
}
