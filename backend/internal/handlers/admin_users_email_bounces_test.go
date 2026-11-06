package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

func TestIntegration_ListEmailBounces(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	ctx := context.Background()
	h := NewAdminUsersHandler(e.db, e.cfg, zerolog.Nop())

	send := func(clubID string, userID *string, recipient, provider, status, detail string, created time.Time) string {
		t.Helper()
		var id string
		if err := e.db.QueryRow(ctx,
			`INSERT INTO email_sends (club_id, user_id, kind, recipient, recipient_domain, provider, status,
			                          outcome_detail, created_at, last_event_at)
			 VALUES ($1, $2, 'invoice', $3, split_part($3, '@', 2), $4, $5, NULLIF($6, ''), $7::timestamptz, $7::timestamptz + interval '1 minute')
			 RETURNING id`,
			clubID, userID, recipient, provider, status, detail, created,
		).Scan(&id); err != nil {
			t.Fatalf("insert email send: %v", err)
		}
		return id
	}
	member := func() (string, string) {
		t.Helper()
		return testutil.SeedUser(t, e.db, e.clubID, []string{"member"})
	}
	t0 := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)

	// A bounce, then a reminder still in flight: the pending row does not
	// hide the bounce.
	pending, pendingEmail := member()
	send(e.clubID, &pending, pendingEmail, "stalwart", "rejected", "550 5.1.1 no such user", t0)
	send(e.clubID, &pending, pendingEmail, "stalwart", "submitted", "", t0.Add(time.Hour))

	// A faktura to a typed address carries no user, and the case differs.
	typed, typedEmail := member()
	send(e.clubID, nil, strings.ToUpper(typedEmail), "stalwart", "rejected", "550 5.4.1 Access denied", t0)

	// A later delivery clears the flag.
	recovered, recoveredEmail := member()
	send(e.clubID, &recovered, recoveredEmail, "stalwart", "rejected", "550 5.1.1 no such user", t0)
	send(e.clubID, &recovered, recoveredEmail, "stalwart", "accepted", "250 ok", t0.Add(time.Hour))

	// A corrected address clears it too.
	moved, movedEmail := member()
	send(e.clubID, &moved, movedEmail, "stalwart", "rejected", "550 5.1.1 no such user", t0)
	if _, err := e.db.Exec(ctx, `UPDATE users SET email = 'flyttet-'||email WHERE id = $1`, moved); err != nil {
		t.Fatal(err)
	}

	// An SES bounce has no outcome_detail; its permanent-bounce diagnostic
	// stands in.
	ses, sesEmail := member()
	sesSend := send(e.clubID, &ses, sesEmail, "ses", "rejected", "", t0)
	if _, err := e.db.Exec(ctx,
		`INSERT INTO mail_delivery_events (send_id, ses_message_id, event_type, event_at, recipient,
		                                   bounce_type, bounce_subtype, diagnostic, raw)
		 VALUES ($1, 'synthetic-msg-ses', 'bounce', $2, $3, 'Permanent', 'General', 'smtp; 550 5.1.1 user unknown', '{}')`,
		sesSend, t0.Add(time.Minute), sesEmail); err != nil {
		t.Fatal(err)
	}

	// A spam complaint is flagged as well.
	complained, complainedEmail := member()
	send(e.clubID, &complained, complainedEmail, "ses", "complaint", "", t0)

	// A temporary rejection after a hard one is an outcome too: it clears.
	softened, softenedEmail := member()
	send(e.clubID, &softened, softenedEmail, "stalwart", "rejected", "550 5.1.1 no such user", t0)
	send(e.clubID, &softened, softenedEmail, "stalwart", "rejected_temporary", "451 4.3.0 try later", t0.Add(time.Hour))

	// Another member's send that went to this member's address is not theirs.
	addressee, addresseeEmail := member()
	sender, _ := member()
	send(e.clubID, &sender, addresseeEmail, "stalwart", "rejected", "550 5.1.1 no such user", t0)

	// The system actor is not a member.
	send(e.clubID, &e.systemID, accounting.SystemActorEmail, "stalwart", "rejected", "550 nope", t0)

	// A suppressed send is listed.
	blocked, blockedEmail := member()
	send(e.clubID, &blocked, blockedEmail, "ses", "suppressed", "", t0)

	// The latest bounce has no Permanent event of its own: its detail is
	// empty, not an earlier send's diagnostic.
	again, againEmail := member()
	firstBounce := send(e.clubID, &again, againEmail, "ses", "rejected", "", t0)
	if _, err := e.db.Exec(ctx,
		`INSERT INTO mail_delivery_events (send_id, ses_message_id, event_type, event_at, recipient,
		                                   bounce_type, bounce_subtype, diagnostic, raw)
		 VALUES ($1, 'synthetic-msg-first', 'bounce', $2, $3, 'Permanent', 'General', 'smtp; 550 earlier', '{}')`,
		firstBounce, t0.Add(time.Minute), againEmail); err != nil {
		t.Fatal(err)
	}
	send(e.clubID, &again, againEmail, "ses", "rejected", "", t0.Add(time.Hour))

	// Another club's bounced member is not listed.
	otherClub := testutil.SeedClub(t, e.db)
	otherMember, otherEmail := testutil.SeedUser(t, e.db, otherClub, []string{"member"})
	send(otherClub, &otherMember, otherEmail, "stalwart", "rejected", "550 nope", t0)

	rec := e.do(t, http.MethodGet, "/admin/users/email-bounces", "/admin/users/email-bounces", h.HandleListEmailBounces, "")
	assertStatus(t, rec, http.StatusOK)
	var body struct {
		Bounces map[string]emailBounce `json:"bounces"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct{ status, detail string }{
		pending:    {"rejected", "550 5.1.1 no such user"},
		typed:      {"rejected", "550 5.4.1 Access denied"},
		ses:        {"rejected", "smtp; 550 5.1.1 user unknown"},
		complained: {"complaint", ""},
		blocked:    {"suppressed", ""},
		again:      {"rejected", ""},
	}
	if len(body.Bounces) != len(want) {
		t.Fatalf("bounces = %+v, want members %v", body.Bounces, want)
	}
	for id, w := range want {
		got, ok := body.Bounces[id]
		if !ok || got.Status != w.status || got.Detail != w.detail || got.At.IsZero() {
			t.Errorf("bounce for %s = %+v (listed %v), want %+v", id, got, ok, w)
		}
	}
	for _, id := range []string{recovered, moved, otherMember, softened, addressee, sender, e.systemID} {
		if _, ok := body.Bounces[id]; ok {
			t.Errorf("member %s listed, want cleared or out of club", id)
		}
	}
}
