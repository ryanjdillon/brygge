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

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/testutil"
)

type fakeSuppression struct {
	removed bool
	err     error
	calls   []string
}

func (f *fakeSuppression) Remove(_ context.Context, address string) (bool, error) {
	f.calls = append(f.calls, address)
	return f.removed, f.err
}

func TestMergeSendsDropsJournalSendsCoveredByTheLog(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	legacy := []deliveryLogSend{
		{Kind: "sent", At: base},
		{Kind: "reminder", At: base.Add(24 * time.Hour)},
		// The same mail as the first row: stamped a moment before it.
		{Kind: "resent", At: base.Add(48*time.Hour - 10*time.Second)},
	}
	rows := []tableSend{
		{id: "a", kind: "invoice", provider: "ses", status: "accepted", createdAt: base.Add(48 * time.Hour)},
		{id: "b", kind: "invoice", provider: "stalwart", status: "submitted", createdAt: base.Add(49 * time.Hour)},
		{id: "c", kind: "invoice", provider: "stalwart", status: "failed", createdAt: base.Add(50 * time.Hour)},
		{id: "d", kind: "invoice_reminder", provider: "ses", status: "suppressed", createdAt: base.Add(51 * time.Hour)},
	}
	sends, journalIdx := mergeSends(legacy, rows, map[string][]deliveryLogEvent{})

	got := []string{}
	for _, s := range sends {
		got = append(got, s.ID+":"+s.Kind)
	}
	want := []string{":sent", ":reminder", "a:resent", "b:resent", "c:resent", "d:reminder"}
	if len(got) != len(want) {
		t.Fatalf("sends = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sends = %v, want %v", got, want)
		}
	}
	// Journal: both journal-era sends and the Stalwart row the relay took;
	// never an SES row or a send the relay refused.
	if len(journalIdx) != 3 || journalIdx[0] != 0 || journalIdx[1] != 1 || journalIdx[2] != 3 {
		t.Fatalf("journalIdx = %v, want [0 1 3]", journalIdx)
	}
	if !sends[5].CanUnsuppress || sends[2].CanUnsuppress {
		t.Fatalf("can_unsuppress: suppressed=%v accepted=%v", sends[5].CanUnsuppress, sends[2].CanUnsuppress)
	}

	// Without journal-era sends, the first faktura row is the first send.
	sends, journalIdx = mergeSends(nil, rows[:1], nil)
	if sends[0].Kind != "sent" || len(journalIdx) != 0 {
		t.Fatalf("first row = %q, journalIdx %v", sends[0].Kind, journalIdx)
	}
}

func TestCanUnsuppress(t *testing.T) {
	t.Parallel()
	cases := []struct {
		provider, status string
		hardBounced      bool
		want             bool
	}{
		{"ses", "suppressed", false, true},
		{"ses", "rejected", true, true},
		{"ses", "rejected", false, false}, // an SES reject, not a bounce
		{"ses", "complaint", true, false}, // the recipient reported spam
		{"ses", "accepted", false, false},
		{"stalwart", "suppressed", false, false},
	}
	for _, c := range cases {
		if got := canUnsuppress(c.provider, c.status, c.hardBounced); got != c.want {
			t.Errorf("canUnsuppress(%s, %s, %v) = %v, want %v", c.provider, c.status, c.hardBounced, got, c.want)
		}
	}
}

func TestSMTPReplyCode(t *testing.T) {
	t.Parallel()
	for diag, want := range map[string]int{
		"smtp; 550 5.1.1 user unknown": 550,
		"421 try later":                421,
		"Amazon SES did not send":      0,
		"":                             0,
	} {
		if got := smtpReplyCode(diag); got != want {
			t.Errorf("smtpReplyCode(%q) = %d, want %d", diag, got, want)
		}
	}
}

// seedEmailSend inserts an email_sends row for a faktura and returns its id.
func seedEmailSend(t *testing.T, e *systemActorEnv, invoiceID, kind, provider, status string, at time.Time) string {
	t.Helper()
	var id string
	if err := e.db.QueryRow(context.Background(),
		`INSERT INTO email_sends (club_id, kind, recipient, recipient_domain, related_type, related_id,
		                          provider, status, created_at)
		 VALUES ($1,$2,'medlem@example.org','example.org','invoice',$3,$4,$5,$6)
		 RETURNING id`,
		e.clubID, kind, invoiceID, provider, status, at,
	).Scan(&id); err != nil {
		t.Fatalf("insert email send: %v", err)
	}
	return id
}

func seedDeliveryEvent(t *testing.T, e *systemActorEnv, sendID, msgID, typ, bounceType, bounceSub, diag string, at time.Time) {
	t.Helper()
	if _, err := e.db.Exec(context.Background(),
		`INSERT INTO mail_delivery_events (send_id, ses_message_id, event_type, event_at, recipient,
		                                   bounce_type, bounce_subtype, diagnostic, remote_mta, raw)
		 VALUES ($1,$2,$3,$4,'medlem@example.org',NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),
		         CASE WHEN $3 = 'send' THEN NULL ELSE 'mx.example.org' END, '{}')`,
		sendID, msgID, typ, at, bounceType, bounceSub, diag); err != nil {
		t.Fatalf("insert delivery event: %v", err)
	}
}

func TestIntegration_DeliveryLogTablePath(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	ctx := context.Background()
	periodID := seedFiscalPeriod(t, e.db, e.clubID, 2026)
	userID, _ := testutil.SeedUser(t, e.db, e.clubID, []string{"member"})

	// First send before the send log existed, then a resend and a reminder
	// recorded in email_sends through SES.
	sentAt := time.Now().Add(-5 * time.Hour).UTC().Truncate(time.Second)
	resentAt := sentAt.Add(2 * time.Hour)
	remindedAt := sentAt.Add(3 * time.Hour)
	kid, _ := finance.GenerateKIDMethod("000", 78, 1, finance.KIDMethodMod10)
	var invoiceID string
	if err := e.db.QueryRow(ctx,
		`INSERT INTO invoices
		   (club_id, user_id, invoice_number, kid_number, due_date,
		    total_amount, fiscal_period_id, recipient_kind, recipient_email, sent_at)
		 VALUES ($1,$2,78,$3,'2026-12-01',450.00,$4,'private','medlem@example.org',$5)
		 RETURNING id`,
		e.clubID, userID, kid, periodID, sentAt,
	).Scan(&invoiceID); err != nil {
		t.Fatalf("insert invoice: %v", err)
	}
	// The resend's audit row describes the same mail as its email_sends row.
	if _, err := e.db.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details, created_at)
		 VALUES ($1,$2,'invoice.emailed','invoice',$3,'{"resend":true}',$4)`,
		e.clubID, e.adminID, invoiceID, resentAt.Add(-2*time.Second)); err != nil {
		t.Fatalf("insert audit row: %v", err)
	}
	resendID := seedEmailSend(t, e, invoiceID, "invoice", "ses", "accepted", resentAt)
	seedDeliveryEvent(t, e, resendID, "msg-1", "send", "", "", "", resentAt.Add(time.Second))
	seedDeliveryEvent(t, e, resendID, "msg-1", "delivery", "", "", "250 2.0.0 OK", resentAt.Add(3*time.Second))
	reminderID := seedEmailSend(t, e, invoiceID, "invoice_reminder", "ses", "suppressed", remindedAt)
	seedDeliveryEvent(t, e, reminderID, "msg-2", "bounce", "Permanent", "OnAccountSuppressionList",
		"Amazon SES did not send the message to this address because it is on the suppression list for your account.",
		remindedAt.Add(time.Second))

	at := func(t time.Time) string { return strconv.FormatInt(t.UnixMicro(), 10) }
	journal := &fakeMailJournal{events: []mailEvent{
		{RealtimeUsec: at(sentAt), Message: mailEvQueued, SpanID: "in1", QueueID: "11"},
		{RealtimeUsec: at(sentAt.Add(time.Second)), Message: mailEvAttemptStarted, SpanID: "a1", QueueID: "11"},
		{RealtimeUsec: at(sentAt.Add(2 * time.Second)), Message: mailEvDelivered, SpanID: "a1", Code: "250"},
	}}
	h := NewInvoiceHandler(e.db, e.cfg, nil, nil, nil, nil, zerolog.Nop())
	h.mailJournal = journal

	rec := e.do(t, http.MethodGet, "/invoices/{invoiceID}/delivery-log", "/invoices/"+invoiceID+"/delivery-log", h.HandleGetDeliveryLog, "")
	assertStatus(t, rec, http.StatusOK)
	var body struct {
		Available bool              `json:"available"`
		Sends     []deliveryLogSend `json:"sends"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, s := range body.Sends {
		got = append(got, s.Kind+":"+s.Status)
	}
	want := []string{"reminder:" + deliverySuppressed, "resent:" + deliveryAccepted, "sent:" + deliveryAccepted}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("sends = %v, want %v", got, want)
	}
	if !body.Available {
		t.Fatal("journal reported unavailable")
	}
	reminder, resend := body.Sends[0], body.Sends[1]
	if reminder.ID != reminderID || !reminder.CanUnsuppress || reminder.Provider != "ses" {
		t.Fatalf("reminder = %+v", reminder)
	}
	if len(reminder.Events) != 1 || reminder.Events[0].Type != "bounce" ||
		reminder.Events[0].Subtype != "Permanent/OnAccountSuppressionList" {
		t.Fatalf("reminder events = %+v", reminder.Events)
	}
	if resend.CanUnsuppress || resend.SMTPCode != 250 || resend.RemoteHost != "mx.example.org" || len(resend.Events) != 2 {
		t.Fatalf("resend = %+v", resend)
	}

	// The list badge reads the latest send per faktura in one query.
	rec = e.do(t, http.MethodGet, "/invoices/delivery-statuses", "/invoices/delivery-statuses", h.HandleGetDeliveryStatuses, "")
	assertStatus(t, rec, http.StatusOK)
	var statuses struct {
		Statuses map[string]struct {
			Status string `json:"status"`
		} `json:"statuses"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &statuses); err != nil {
		t.Fatal(err)
	}
	if statuses.Statuses[invoiceID].Status != deliverySuppressed || len(statuses.Statuses) != 1 {
		t.Fatalf("statuses = %+v", statuses.Statuses)
	}
	other := newSystemActorEnv(t)
	rec = other.do(t, http.MethodGet, "/invoices/delivery-statuses", "/invoices/delivery-statuses", h.HandleGetDeliveryStatuses, "")
	assertStatus(t, rec, http.StatusOK)
	statuses.Statuses = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &statuses); err != nil {
		t.Fatal(err)
	}
	if len(statuses.Statuses) != 0 {
		t.Fatalf("other club sees %+v", statuses.Statuses)
	}
}

func TestIntegration_UnsuppressRecipient(t *testing.T) {
	t.Parallel()
	e := newSystemActorEnv(t)
	ctx := context.Background()
	periodID := seedFiscalPeriod(t, e.db, e.clubID, 2026)
	kid, _ := finance.GenerateKIDMethod("000", 79, 1, finance.KIDMethodMod10)
	var invoiceID string
	if err := e.db.QueryRow(ctx,
		`INSERT INTO invoices
		   (club_id, invoice_number, kid_number, due_date,
		    total_amount, fiscal_period_id, recipient_kind, recipient_email, sent_at)
		 VALUES ($1,79,$2,'2026-12-01',450.00,$3,'private','medlem@example.org',now())
		 RETURNING id`,
		e.clubID, kid, periodID,
	).Scan(&invoiceID); err != nil {
		t.Fatalf("insert invoice: %v", err)
	}
	now := time.Now().UTC()
	suppressedID := seedEmailSend(t, e, invoiceID, "invoice", "ses", "suppressed", now.Add(-time.Hour))
	acceptedID := seedEmailSend(t, e, invoiceID, "invoice", "ses", "accepted", now)
	path := func(sendID string) string {
		return "/invoices/" + invoiceID + "/email-sends/" + sendID + "/unsuppress"
	}
	const pattern = "/invoices/{invoiceID}/email-sends/{sendID}/unsuppress"

	h := NewInvoiceHandler(e.db, e.cfg, nil, audit.NewService(e.db, zerolog.Nop()), nil, nil, zerolog.Nop())

	// Without SES there is nothing to call.
	rec := e.do(t, http.MethodPost, pattern, path(suppressedID), h.HandleUnsuppressRecipient, "")
	assertStatus(t, rec, http.StatusServiceUnavailable)

	fake := &fakeSuppression{removed: true}
	h.SetSuppressionList(fake)

	// A delivered send was not stopped by the list.
	rec = e.do(t, http.MethodPost, pattern, path(acceptedID), h.HandleUnsuppressRecipient, "")
	assertStatus(t, rec, http.StatusConflict)

	rec = e.do(t, http.MethodPost, pattern, path(suppressedID), h.HandleUnsuppressRecipient, "")
	assertStatus(t, rec, http.StatusOK)
	if len(fake.calls) != 1 || fake.calls[0] != "medlem@example.org" {
		t.Fatalf("SES calls = %v", fake.calls)
	}
	var n int
	if err := e.db.QueryRow(ctx,
		`SELECT count(*) FROM audit_log
		  WHERE club_id = $1 AND action = $2 AND resource = 'invoice' AND resource_id = $3
		    AND details->>'send_id' = $4`,
		e.clubID, audit.ActionInvoiceRecipientUnsuppressed, invoiceID, suppressedID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("audit rows = %d, want 1", n)
	}

	// An SES error is reported, not swallowed.
	fake.err = errors.New("throttled")
	rec = e.do(t, http.MethodPost, pattern, path(suppressedID), h.HandleUnsuppressRecipient, "")
	assertStatus(t, rec, http.StatusBadGateway)

	// Another club's admin cannot reach the send.
	other := newSystemActorEnv(t)
	fake.err = nil
	rec = other.do(t, http.MethodPost, pattern, path(suppressedID), h.HandleUnsuppressRecipient, "")
	assertStatus(t, rec, http.StatusNotFound)
	if len(fake.calls) != 2 {
		t.Fatalf("SES calls = %v, want 2", fake.calls)
	}
}
