package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

type fakeMailJournal struct {
	events    []mailEvent
	err       error
	recipient string
}

func (f *fakeMailJournal) Events(_ context.Context, recipient string) ([]mailEvent, error) {
	f.recipient = recipient
	return f.events, f.err
}

func TestIntegration_DeliveryLogMatchesSends(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	ctx := context.Background()
	periodID := seedFiscalPeriod(t, e.db, e.clubID, 2026)
	userID, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})

	sentAt := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	kid, _ := finance.GenerateKIDMethod("000", 77, 1, finance.KIDMethodMod10)
	var invoiceID string
	if err := e.db.QueryRow(ctx,
		`INSERT INTO invoices
		   (club_id, user_id, invoice_number, kid_number, due_date,
		    total_amount, fiscal_period_id, recipient_kind, recipient_email, sent_at)
		 VALUES ($1,$2,77,$3,'2026-12-01',450.00,$4,'private','medlem@example.org',$5)
		 RETURNING id`,
		e.clubID, userID, kid, periodID, sentAt,
	).Scan(&invoiceID); err != nil {
		t.Fatalf("insert invoice: %v", err)
	}
	reminderAt := sentAt.Add(time.Hour)
	for _, a := range []struct {
		action, details string
		at              time.Time
	}{
		{"invoice.emailed", `{"resend":false}`, sentAt.Add(time.Second)}, // the first send: already sent_at
		{"invoice.reminded", `{}`, reminderAt},
		{"invoice.emailed", `{"resend":true}`, sentAt.Add(2 * time.Hour)},
	} {
		if _, err := e.db.Exec(ctx,
			`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details, created_at)
			 VALUES ($1,$2,$3,'invoice',$4,$5,$6)`,
			e.clubID, e.adminID, a.action, invoiceID, a.details, a.at); err != nil {
			t.Fatalf("insert audit row: %v", err)
		}
	}

	at := func(t time.Time) string { return strconv.FormatInt(t.UnixMicro(), 10) }
	journal := &fakeMailJournal{events: []mailEvent{
		{RealtimeUsec: at(sentAt.Add(-time.Second)), Message: mailEvQueued, SpanID: "in1", QueueID: "11"},
		{RealtimeUsec: at(sentAt), Message: mailEvAttemptStarted, SpanID: "a1", QueueID: "11"},
		{RealtimeUsec: at(sentAt.Add(time.Second)), Message: mailEvDelivered, SpanID: "a1", Code: "250", Hostname: "mx.example.org"},
		{RealtimeUsec: at(reminderAt.Add(-time.Second)), Message: mailEvQueued, SpanID: "in2", QueueID: "12"},
		{RealtimeUsec: at(reminderAt), Message: mailEvAttemptStarted, SpanID: "a2", QueueID: "12"},
		{RealtimeUsec: at(reminderAt.Add(time.Second)), Message: mailEvRcptRejected, SpanID: "a2", Code: "450"},
	}}
	h := NewInvoiceHandler(e.db, e.cfg, nil, nil, nil, nil, zerolog.Nop())
	h.mailJournal = journal

	rec := e.do(t, http.MethodGet, "/invoices/{invoiceID}/delivery-log", "/invoices/"+invoiceID+"/delivery-log", h.HandleGetDeliveryLog, "")
	assertStatus(t, rec, http.StatusOK)
	var body struct {
		Available bool              `json:"available"`
		Recipient string            `json:"recipient_email"`
		Sends     []deliveryLogSend `json:"sends"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if journal.recipient != "medlem@example.org" || !body.Available {
		t.Fatalf("journal asked for %q, available=%v", journal.recipient, body.Available)
	}
	// Newest first: resend (never reached the mail server), reminder, first send.
	got := []string{}
	for _, s := range body.Sends {
		got = append(got, s.Kind+":"+s.Status)
	}
	want := []string{"resent:" + deliveryNoRecord, "reminder:" + deliveryDeferred, "sent:" + deliveryAccepted}
	if len(got) != len(want) {
		t.Fatalf("sends = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sends = %v, want %v", got, want)
		}
	}

	// A failing helper reports unavailable, not an empty log.
	h.mailJournal = &fakeMailJournal{err: errors.New("helper down")}
	rec = e.do(t, http.MethodGet, "/invoices/{invoiceID}/delivery-log", "/invoices/"+invoiceID+"/delivery-log", h.HandleGetDeliveryLog, "")
	assertStatus(t, rec, http.StatusOK)
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Available || len(body.Sends) != 3 || body.Sends[0].Status != deliveryUnavailable {
		t.Fatalf("helper failure = available %v, sends %+v", body.Available, body.Sends)
	}

	// Another club's admin cannot read it.
	other := newSystemActorEnv(t)
	rec = other.do(t, http.MethodGet, "/invoices/{invoiceID}/delivery-log", "/invoices/"+invoiceID+"/delivery-log", h.HandleGetDeliveryLog, "")
	assertStatus(t, rec, http.StatusNotFound)
}
