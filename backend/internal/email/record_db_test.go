package email_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

type sendRow struct {
	clubID, kind, recipientDomain, provider, status string
	userID, relatedType, relatedID, errText         *string
}

func onlySend(t *testing.T, pool *pgxpool.Pool) sendRow {
	t.Helper()
	var r sendRow
	if err := pool.QueryRow(context.Background(), `
		SELECT club_id::text, kind, recipient_domain, provider, status,
		       user_id::text, related_type, related_id::text, error
		  FROM email_sends`,
	).Scan(&r.clubID, &r.kind, &r.recipientDomain, &r.provider, &r.status,
		&r.userID, &r.relatedType, &r.relatedID, &r.errText); err != nil {
		t.Fatalf("read email_sends: %v", err)
	}
	return r
}

func TestPGSendLogRecordsOutcome(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	userID, _ := testutil.SeedUser(t, pool, clubID, []string{"member"})
	const invoiceID = "7a0d9c4e-1b2f-4c3d-8e5f-6a7b8c9d0e1f"

	for _, tc := range []struct {
		name       string
		err        error
		wantStatus string
	}{
		{"submitted", nil, email.SendStatusSubmitted},
		{"failed", errors.New("smtp RCPT TO: 550 5.1.1"), email.SendStatusFailed},
		{"unknown", fmt.Errorf("smtp close DATA: %w: EOF", email.ErrDeliveryAmbiguous), email.SendStatusUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `DELETE FROM email_sends`); err != nil {
				t.Fatal(err)
			}
			log := email.NewSendLog(pool, "")
			s := email.Record(&email.MockSender{Err: tc.err}, email.ProviderSES, log, zerolog.Nop())
			sctx := email.WithSendMeta(ctx, email.SendMeta{
				ClubID: clubID, Kind: email.KindInvoice, UserID: userID,
				RelatedType: "invoice", RelatedID: invoiceID,
			})
			if err := s.Send(sctx, "Member@Example.NET", "s", "b"); !errors.Is(err, tc.err) {
				t.Fatalf("err = %v", err)
			}
			r := onlySend(t, pool)
			if r.clubID != clubID || r.kind != "invoice" || r.recipientDomain != "example.net" ||
				r.provider != "ses" || r.status != tc.wantStatus {
				t.Errorf("row = %+v", r)
			}
			if r.userID == nil || *r.userID != userID || r.relatedType == nil || *r.relatedType != "invoice" ||
				r.relatedID == nil || *r.relatedID != invoiceID {
				t.Errorf("links = user %v related %v/%v", r.userID, r.relatedType, r.relatedID)
			}
			if (r.errText != nil) != (tc.err != nil) {
				t.Errorf("error = %v for %v", r.errText, tc.err)
			}
		})
	}
}

// A send without a club id is filed under the CLUB_SLUG club; with no such
// club it still goes out, unrecorded.
func TestPGSendLogResolvesInstanceClub(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	var slug string
	if err := pool.QueryRow(ctx, `SELECT slug FROM clubs WHERE id = $1`, clubID).Scan(&slug); err != nil {
		t.Fatal(err)
	}

	mock := &email.MockSender{}
	s := email.Record(mock, email.ProviderStalwart, email.NewSendLog(pool, "no-such-club"), zerolog.Nop())
	if err := s.Send(email.WithKind(ctx, email.KindContact), "board@example.org", "s", "b"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_sends`).Scan(&n); err != nil || n != 0 || len(mock.Calls) != 1 {
		t.Fatalf("unresolvable club: rows %d (%v), sends %d; want 0 rows, 1 send", n, err, len(mock.Calls))
	}

	s = email.Record(mock, email.ProviderStalwart, email.NewSendLog(pool, slug), zerolog.Nop())
	if err := s.Send(email.WithKind(ctx, email.KindContact), "board@example.org", "s", "b"); err != nil {
		t.Fatal(err)
	}
	r := onlySend(t, pool)
	if r.clubID != clubID || r.kind != "contact" || r.provider != "stalwart" || r.status != "submitted" {
		t.Errorf("row = %+v", r)
	}
}

// A delivery event that lands before the sender's own update keeps its
// status: the sender only moves a row out of sending.
func TestPGSendLogFinishDoesNotRegressEventStatus(t *testing.T) {
	testutil.SkipIfNoDB(t)
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()
	clubID := testutil.SeedClub(t, pool)
	log := email.NewSendLog(pool, "")
	const id = "5b6c7d8e-9f0a-4b1c-8d2e-3f4a5b6c7d8e"

	if err := log.Start(ctx, email.LoggedSend{ID: id, Meta: email.SendMeta{ClubID: clubID, Kind: email.KindMagicLink},
		Provider: email.ProviderSES, Recipient: "a@example.net"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE email_sends SET status = 'accepted' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if err := log.Finish(ctx, id, email.SendStatusSubmitted, "msg-1", ""); err != nil {
		t.Fatal(err)
	}
	if r := onlySend(t, pool); r.status != "accepted" {
		t.Errorf("status = %s, want the event's accepted kept", r.status)
	}
}
