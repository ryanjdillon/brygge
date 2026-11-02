package handlers

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var mailT0 = time.Date(2026, 11, 1, 18, 0, 0, 0, time.UTC)

func ev(offset time.Duration, msg string, kv ...string) mailEvent {
	e := mailEvent{RealtimeUsec: strconv.FormatInt(mailT0.Add(offset).UnixMicro(), 10), Message: msg}
	for i := 0; i+1 < len(kv); i += 2 {
		switch kv[i] {
		case "span":
			e.SpanID = kv[i+1]
		case "queue":
			e.QueueID = kv[i+1]
		case "to":
			e.To = kv[i+1]
		case "host":
			e.Hostname = kv[i+1]
		case "code":
			e.Code = kv[i+1]
		case "details":
			e.Details = kv[i+1]
		case "caused":
			e.CausedBy = kv[i+1]
		case "next":
			e.NextRetry = kv[i+1]
		}
	}
	return e
}

// greylisted is the 2026-11-01 shape: 450 on the first attempt, rescheduled,
// delivered on the second (synthetic address and hosts).
func greylisted(queue string, at time.Duration) []mailEvent {
	return []mailEvent{
		ev(at, mailEvQueued, "span", "in1", "queue", queue, "to", "[medlem@example.org]"),
		ev(at+time.Second, mailEvAttemptStarted, "span", "a1", "queue", queue, "to", "[medlem@example.org]"),
		ev(at+2*time.Second, mailEvRcptRejected, "span", "a1", "to", "medlem@example.org", "host", "mx.example.org", "code", "450", "details", "4.7.1 greylisting in action"),
		ev(at+3*time.Second, mailEvRescheduled, "span", "a1", "next", strconv.FormatInt(mailT0.Add(at+5*time.Minute).Unix(), 10)),
		ev(at+5*time.Minute, mailEvAttemptStarted, "span", "a2", "queue", queue, "to", "[medlem@example.org]"),
		ev(at+5*time.Minute+time.Second, mailEvDelivered, "span", "a2", "to", "medlem@example.org", "host", "mx.example.org", "code", "250", "details", "2.0.0 Ok: queued"),
		ev(at+5*time.Minute+2*time.Second, mailEvCompleted, "span", "a2"),
	}
}

func TestGroupSubmissionsJoinsAttemptsBySpan(t *testing.T) {
	subs := groupSubmissions(greylisted("101", 0))
	if len(subs) != 1 {
		t.Fatalf("submissions = %d, want 1", len(subs))
	}
	s := subs[0]
	if s.QueueID != "101" || !s.SubmittedAt.Equal(mailT0) {
		t.Fatalf("submission = %s at %v", s.QueueID, s.SubmittedAt)
	}
	if len(s.Events) != 7 {
		t.Fatalf("events = %d, want 7 (span-only events joined through the attempt)", len(s.Events))
	}
}

func TestSubmissionOutcome(t *testing.T) {
	retryAt := mailT0.Add(10 * time.Minute)
	cases := []struct {
		name   string
		events []mailEvent
		status string
		code   int
		retry  bool
	}{
		{"greylisted then delivered", greylisted("1", 0), deliveryAccepted, 250, false},
		{"deferred with retry", []mailEvent{
			ev(0, mailEvQueued, "span", "in", "queue", "1"),
			ev(time.Second, mailEvAttemptStarted, "span", "a", "queue", "1"),
			ev(2*time.Second, mailEvRcptRejected, "span", "a", "code", "451", "host", "mx.example.org"),
			ev(3*time.Second, mailEvRescheduled, "span", "a", "next", strconv.FormatInt(retryAt.Unix(), 10)),
		}, deliveryDeferred, 451, true},
		{"connect error has no code", []mailEvent{
			ev(0, mailEvQueued, "span", "in", "queue", "1"),
			ev(time.Second, mailEvAttemptStarted, "span", "a", "queue", "1"),
			ev(2*time.Second, "Connection error", "span", "a", "caused", "connection refused"),
		}, deliveryDeferred, 0, false},
		{"5xx rcpt then bounce", []mailEvent{
			ev(0, mailEvQueued, "span", "in", "queue", "1"),
			ev(time.Second, mailEvAttemptStarted, "span", "a", "queue", "1"),
			ev(2*time.Second, mailEvRcptRejected, "span", "a", "code", "550", "details", "5.1.1 no such user"),
			ev(3*time.Second, mailEvDsnPermFail, "span", "a", "details", "Unexpected response for RCPT TO: 550"),
		}, deliveryRejected, 0, false},
		{"5xx without dsn", []mailEvent{
			ev(0, mailEvQueued, "span", "in", "queue", "1"),
			ev(time.Second, mailEvAttemptStarted, "span", "a", "queue", "1"),
			ev(2*time.Second, mailEvMessageRejected, "span", "a", "code", "554"),
		}, deliveryRejected, 554, false},
		{"queued only", []mailEvent{ev(0, mailEvQueued, "span", "in", "queue", "1")}, deliveryQueued, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subs := groupSubmissions(tc.events)
			if len(subs) != 1 {
				t.Fatalf("submissions = %d", len(subs))
			}
			out := subs[0].outcome()
			if out.Status != tc.status || out.SMTPCode != tc.code {
				t.Fatalf("outcome = %s/%d, want %s/%d", out.Status, out.SMTPCode, tc.status, tc.code)
			}
			if (out.NextRetryAt != nil) != tc.retry {
				t.Fatalf("next retry = %v, want set=%v", out.NextRetryAt, tc.retry)
			}
			if tc.retry && !out.NextRetryAt.Equal(retryAt) {
				t.Fatalf("next retry = %v, want %v", out.NextRetryAt, retryAt)
			}
		})
	}
}

func TestMatchSends(t *testing.T) {
	events := append(greylisted("1", 0),
		// A login link to the same address an hour later: not this faktura.
		ev(time.Hour, mailEvQueued, "span", "in2", "queue", "2"),
		ev(time.Hour+time.Second, mailEvAttemptStarted, "span", "b", "queue", "2"),
		ev(time.Hour+2*time.Second, mailEvDelivered, "span", "b", "code", "250"),
	)
	sends := []deliveryLogSend{
		{Kind: "sent", At: mailT0.Add(2 * time.Second)},
		{Kind: "reminder", At: mailT0.Add(-24 * time.Hour)},         // never reached the mail server
		{Kind: "reminder", At: mailT0.Add(-40 * 24 * time.Hour)},    // older than the journal
		{Kind: "resent", At: mailT0.Add(2*time.Hour - time.Minute)}, // just now, not logged yet
	}
	matchSends(sends, groupSubmissions(events), mailT0.Add(2*time.Hour))

	want := []string{deliveryAccepted, deliveryNoRecord, deliveryBeyondRetention, deliveryPending}
	for i, w := range want {
		if sends[i].Status != w {
			t.Errorf("send %d (%s) status = %s, want %s", i, sends[i].Kind, sends[i].Status, w)
		}
		if sends[i].Events == nil {
			t.Errorf("send %d events is nil, want a JSON array", i)
		}
	}
	if sends[0].QueueID != "1" || sends[0].SMTPCode != 250 || sends[0].RemoteHost != "mx.example.org" {
		t.Errorf("matched send = %+v", sends[0])
	}
}

func TestMatchSendsClaimsEachSubmissionOnce(t *testing.T) {
	events := []mailEvent{
		ev(0, mailEvQueued, "span", "in", "queue", "1"),
		ev(90*time.Second, mailEvQueued, "span", "in2", "queue", "2"),
	}
	sends := []deliveryLogSend{
		{Kind: "sent", At: mailT0.Add(time.Second)},
		{Kind: "resent", At: mailT0.Add(91 * time.Second)},
	}
	matchSends(sends, groupSubmissions(events), mailT0.Add(time.Hour))
	if sends[0].QueueID != "1" || sends[1].QueueID != "2" {
		t.Fatalf("queue ids = %q, %q; want 1, 2", sends[0].QueueID, sends[1].QueueID)
	}
}

func TestReadMailEventsRequiresEndMarker(t *testing.T) {
	lines := `{"MESSAGE":"Delivery completed","SPANID":"a"}` + "\n"
	if _, err := readMailEvents(strings.NewReader(lines)); err == nil {
		t.Fatal("answer without end marker accepted")
	}
	got, err := readMailEvents(strings.NewReader(lines + "not json\n" + `{"__end":true}` + "\n"))
	if err != nil || len(got) != 1 {
		t.Fatalf("events = %v, err = %v", got, err)
	}
}

func TestSocketMailJournal(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "mail-log.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	gotRecipient := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, _ := bufio.NewReader(conn).ReadString('\n')
		gotRecipient <- strings.TrimSpace(line)
		fmt.Fprintln(conn, `{"MESSAGE":"Message delivered","SPANID":"a","CODE":"250"}`)
		fmt.Fprintln(conn, `{"__end":true}`)
	}()

	j := &socketMailJournal{path: sock, timeout: 5 * time.Second}
	events, err := j.Events(context.Background(), "medlem@example.org")
	if err != nil {
		t.Fatal(err)
	}
	if r := <-gotRecipient; r != "medlem@example.org" {
		t.Fatalf("helper got %q", r)
	}
	if len(events) != 1 || events[0].code() != 250 {
		t.Fatalf("events = %+v", events)
	}

	missing := &socketMailJournal{path: filepath.Join(t.TempDir(), "none.sock"), timeout: time.Second}
	if _, err := missing.Events(context.Background(), "x@example.org"); err == nil {
		t.Fatal("missing helper socket returned no error")
	}
}
