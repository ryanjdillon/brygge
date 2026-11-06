package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brygge-klubb/brygge/internal/config"
)

// The journal path is read-only legacy since the SES send log (BRY-590): it
// answers only for faktura sends from before email_sends existed and for
// sends that went through Stalwart. Remove this file with the
// brygge-mail-log units 35 days after the SES cutover (BRY-594).

// mailJournal returns the mail server's delivery events for one recipient,
// oldest first. The host's mail-log helper does the journal read: the API
// itself has no journal access (docs/developer/mail/stalwart-internals.md
// § Faktura delivery log).
type mailJournal interface {
	Events(ctx context.Context, recipient string) ([]mailEvent, error)
}

// mailEvent is one Stalwart journal record as the helper relays it. Field
// names are Stalwart's upper-cased event keys; MESSAGE is the event's
// description, which is the only thing that names the event type.
type mailEvent struct {
	RealtimeUsec string `json:"__REALTIME_TIMESTAMP"`
	Message      string `json:"MESSAGE"`
	SpanID       string `json:"SPANID"`
	QueueID      string `json:"QUEUEID"`
	To           string `json:"TO"`
	Hostname     string `json:"HOSTNAME"`
	Code         string `json:"CODE"`
	Details      string `json:"DETAILS"`
	Reason       string `json:"REASON"`
	CausedBy     string `json:"CAUSEDBY"`
	NextRetry    string `json:"NEXTRETRY"`
}

func (e mailEvent) at() time.Time {
	usec, err := strconv.ParseInt(e.RealtimeUsec, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMicro(usec).UTC()
}

func (e mailEvent) code() int {
	n, _ := strconv.Atoi(strings.TrimSpace(e.Code))
	return n
}

func (e mailEvent) detail() string {
	for _, s := range []string{e.Details, e.CausedBy, e.Reason} {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

// Stalwart 0.15 event descriptions (crates/trc/src/event/description.rs).
// They are the contract the helper's journal match and this fold rely on;
// the Stalwart upgrade checklist diffs them.
const (
	mailEvQueued          = "Queued message submission for delivery"
	mailEvAttemptStarted  = "Delivery attempt started"
	mailEvDelivered       = "Message delivered"
	mailEvDsnSuccess      = "DSN success notification"
	mailEvRcptRejected    = "SMTP RCPT TO rejected"
	mailEvMessageRejected = "Message rejected by remote server"
	mailEvRescheduled     = "Message rescheduled for delivery"
	mailEvDsnPermFail     = "DSN permanent failure notification"
	mailEvFailed          = "Delivery failed"
	mailEvCompleted       = "Delivery completed"
)

// mailEvDeferred are attempt errors that leave the message queued for a
// retry unless a 5xx code says otherwise.
var mailEvDeferred = map[string]bool{
	mailEvRcptRejected:                   true,
	mailEvMessageRejected:                true,
	"SMTP RCPT TO failed":                true,
	"Connection error":                   true,
	"MX record lookup failed":            true,
	"IP address lookup failed":           true,
	"SMTP greeting failed":               true,
	"SMTP EHLO rejected":                 true,
	"SMTP MAIL FROM rejected":            true,
	"STARTTLS error":                     true,
	"Implicit TLS error":                 true,
	"DSN temporary failure notification": true,
}

// Delivery outcome of one submission, from the sender's side.
const (
	deliveryAccepted = "accepted" // the recipient's server answered 250 to DATA
	deliveryDeferred = "deferred" // a temporary failure; the mail server retries
	deliveryRejected = "rejected" // a permanent failure; a bounce follows
	deliveryQueued   = "queued"   // submitted, no outcome yet
)

// mailSubmission is one message the API handed to the mail server for the
// recipient, with the events of every delivery attempt.
type mailSubmission struct {
	QueueID     string
	SubmittedAt time.Time
	Events      []mailEvent
}

// groupSubmissions joins the helper's events into submissions. The journal
// carries no span context, so an attempt's outcome events reach their queue
// id only through the attempt's SPANID.
func groupSubmissions(events []mailEvent) []*mailSubmission {
	byQueue := map[string]*mailSubmission{}
	var order []*mailSubmission
	get := func(q string) *mailSubmission {
		s, ok := byQueue[q]
		if !ok {
			s = &mailSubmission{QueueID: q}
			byQueue[q] = s
			order = append(order, s)
		}
		return s
	}
	spanQueue := map[string]string{}
	for _, e := range events {
		if e.Message == mailEvAttemptStarted && e.SpanID != "" && e.QueueID != "" {
			spanQueue[e.SpanID] = e.QueueID
		}
	}
	for _, e := range events {
		q := e.QueueID
		// The submission event's SPANID is the inbound session, not an
		// attempt, so only the queue id places it.
		if e.Message != mailEvQueued {
			if sq, ok := spanQueue[e.SpanID]; ok {
				q = sq
			}
		}
		if q == "" {
			continue
		}
		s := get(q)
		if e.Message == mailEvQueued && s.SubmittedAt.IsZero() {
			s.SubmittedAt = e.at()
		}
		s.Events = append(s.Events, e)
	}
	for _, s := range order {
		sort.SliceStable(s.Events, func(i, j int) bool { return s.Events[i].at().Before(s.Events[j].at()) })
		if s.SubmittedAt.IsZero() && len(s.Events) > 0 {
			s.SubmittedAt = s.Events[0].at()
		}
	}
	return order
}

// mailOutcome is the folded result of one submission.
type mailOutcome struct {
	Status      string
	SMTPCode    int
	RemoteHost  string
	Detail      string
	NextRetryAt *time.Time
	At          *time.Time
	// Final is set when the journal holds a terminal event (delivered, DSN
	// success, DSN permanent failure, delivery failed): no later event can
	// change the outcome.
	Final bool
}

// outcome folds a submission's events. Acceptance wins over everything
// (a later MX may accept after an earlier one refused); a bounce or expiry
// is final otherwise; failing both, the latest attempt error decides
// between deferred and rejected by its SMTP class.
func (s *mailSubmission) outcome() mailOutcome {
	var out mailOutcome
	set := func(status string, e mailEvent) {
		at := e.at()
		out = mailOutcome{Status: status, SMTPCode: e.code(), RemoteHost: e.Hostname, Detail: e.detail(), At: &at}
	}
	for _, e := range s.Events {
		if e.Message == mailEvDelivered || e.Message == mailEvDsnSuccess {
			set(deliveryAccepted, e)
			out.Final = true
			return out
		}
	}
	for _, e := range s.Events {
		if e.Message == mailEvDsnPermFail || e.Message == mailEvFailed {
			set(deliveryRejected, e)
			if s.expired() {
				out.Status = deliveryRejectedTemporary
			}
			out.Final = true
			return out
		}
	}
	out.Status = deliveryQueued
	var retry *time.Time
	for _, e := range s.Events {
		switch {
		case mailEvDeferred[e.Message]:
			status := deliveryDeferred
			if c := e.code(); c >= 500 && c < 600 {
				status = deliveryRejected
			}
			set(status, e)
		case e.Message == mailEvRescheduled:
			if secs, err := strconv.ParseInt(strings.TrimSpace(e.NextRetry), 10, 64); err == nil && secs > 0 {
				t := time.Unix(secs, 0).UTC()
				retry = &t
			}
		}
	}
	if out.Status == deliveryDeferred {
		out.NextRetryAt = retry
	}
	return out
}

// lastAttemptError is the submission's latest attempt error that carries an
// SMTP code: the remote server's own answer, which a DSN or failure event
// only paraphrases.
func (s *mailSubmission) lastAttemptError() (mailEvent, bool) {
	for i := len(s.Events) - 1; i >= 0; i-- {
		if e := s.Events[i]; mailEvDeferred[e.Message] && e.code() != 0 {
			return e, true
		}
	}
	return mailEvent{}, false
}

// expired reports a queue expiry: Stalwart gave up (Delivery failed) while
// the remote server was still answering 4xx. The address may well be valid,
// so it is a temporary rejection, not a hard bounce.
func (s *mailSubmission) expired() bool {
	failed := false
	for _, e := range s.Events {
		if e.Message == mailEvFailed {
			failed = true
			break
		}
	}
	if !failed {
		return false
	}
	e, ok := s.lastAttemptError()
	return ok && e.code() >= 400 && e.code() < 500
}

func mailJournalFromConfig(cfg *config.Config) mailJournal {
	if cfg == nil || cfg.MailLogSocket == "" {
		return nil
	}
	return &socketMailJournal{path: cfg.MailLogSocket, timeout: 20 * time.Second}
}

// socketMailJournal talks to the mail-log helper: write the recipient on one
// line, read JSON lines back. The helper ends a complete answer with a
// {"__end":true} line, so a timeout, a journal error or a killed helper is
// an error here instead of an empty log.
type socketMailJournal struct {
	path    string
	timeout time.Duration
}

var errMailJournalIncomplete = errors.New("mail-log helper ended without a complete answer")

func (j *socketMailJournal) Events(ctx context.Context, recipient string) ([]mailEvent, error) {
	ctx, cancel := context.WithTimeout(ctx, j.timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", j.path)
	if err != nil {
		return nil, fmt.Errorf("dial mail-log helper: %w", err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if _, err := fmt.Fprintf(conn, "%s\n", recipient); err != nil {
		return nil, fmt.Errorf("write to mail-log helper: %w", err)
	}
	if uc, ok := conn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	return readMailEvents(conn)
}

func readMailEvents(r interface{ Read([]byte) (int, error) }) ([]mailEvent, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<16), 1<<20)
	var events []mailEvent
	for scanner.Scan() {
		line := scanner.Bytes()
		var end struct {
			End bool `json:"__end"`
		}
		if json.Unmarshal(line, &end) == nil && end.End {
			return events, nil
		}
		var e mailEvent
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		events = append(events, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read mail-log helper: %w", err)
	}
	return nil, errMailJournalIncomplete
}
