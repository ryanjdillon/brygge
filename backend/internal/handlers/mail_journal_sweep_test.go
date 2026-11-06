package handlers

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// sweepJournal answers per recipient and can act while the sweep waits on
// it, the way a concurrent writer would.
type sweepJournal struct {
	events map[string][]mailEvent
	err    error
	asked  []string
	during func()
}

func (f *sweepJournal) Events(_ context.Context, recipient string) ([]mailEvent, error) {
	f.asked = append(f.asked, recipient)
	if f.during != nil {
		f.during()
	}
	return f.events[recipient], f.err
}

// evAt builds a journal event at an absolute time.
func evAt(at time.Time, msg string, kv ...string) mailEvent {
	e := ev(0, msg, kv...)
	e.RealtimeUsec = strconv.FormatInt(at.UnixMicro(), 10)
	return e
}

// Submission shapes, each under its own queue id; synthetic hosts only.
func subDelivered(q string, at time.Time) []mailEvent {
	return []mailEvent{
		evAt(at, mailEvQueued, "span", "in"+q, "queue", q),
		evAt(at.Add(time.Second), mailEvAttemptStarted, "span", "a"+q, "queue", q),
		evAt(at.Add(2*time.Second), mailEvDelivered, "span", "a"+q, "host", "mx.example.org", "code", "250", "details", "2.0.0 Ok: queued"),
	}
}

func subBounced(q string, at time.Time) []mailEvent {
	return []mailEvent{
		evAt(at, mailEvQueued, "span", "in"+q, "queue", q),
		evAt(at.Add(time.Second), mailEvAttemptStarted, "span", "a"+q, "queue", q),
		evAt(at.Add(2*time.Second), mailEvRcptRejected, "span", "a"+q, "host", "mx.example.org", "code", "550", "details", "5.4.1 Recipient address rejected: Access denied"),
		evAt(at.Add(3*time.Second), mailEvDsnPermFail, "span", "a"+q, "details", "Unexpected response for RCPT TO"),
	}
}

// greylistedAt is a submission whose only attempt got a 450 so far.
func greylistedAt(q string, at time.Time) []mailEvent {
	return []mailEvent{
		evAt(at, mailEvQueued, "span", "in"+q, "queue", q),
		evAt(at.Add(time.Second), mailEvAttemptStarted, "span", "a"+q, "queue", q),
		evAt(at.Add(2*time.Second), mailEvRcptRejected, "span", "a"+q, "code", "450", "details", "4.7.1 greylisted"),
	}
}

func TestSubmissionOutcomeFinalAndExpiry(t *testing.T) {
	t0 := mailT0
	cases := []struct {
		name   string
		events []mailEvent
		status string
		final  bool
	}{
		{"delivered", subDelivered("1", t0), deliveryAccepted, true},
		{"bounced", subBounced("1", t0), deliveryRejected, true},
		{"5xx attempt without dsn", subBounced("1", t0)[:3], deliveryRejected, false},
		{"5xx then a later MX accepts", append(subBounced("1", t0)[:3],
			evAt(t0.Add(time.Minute), mailEvAttemptStarted, "span", "b1", "queue", "1"),
			evAt(t0.Add(time.Minute+time.Second), mailEvDelivered, "span", "b1", "code", "250"),
		), deliveryAccepted, true},
		{"queue expiry after 4xx", []mailEvent{
			evAt(t0, mailEvQueued, "span", "in", "queue", "1"),
			evAt(t0.Add(time.Second), mailEvAttemptStarted, "span", "a", "queue", "1"),
			evAt(t0.Add(2*time.Second), mailEvRcptRejected, "span", "a", "code", "451", "details", "4.7.1 try again later"),
			evAt(t0.Add(5*24*time.Hour), mailEvFailed, "span", "a", "queue", "1"),
		}, deliveryRejectedTemporary, true},
		{"delivery failed after 5xx", []mailEvent{
			evAt(t0, mailEvQueued, "span", "in", "queue", "1"),
			evAt(t0.Add(time.Second), mailEvAttemptStarted, "span", "a", "queue", "1"),
			evAt(t0.Add(2*time.Second), mailEvRcptRejected, "span", "a", "code", "550"),
			evAt(t0.Add(3*time.Second), mailEvFailed, "span", "a", "queue", "1"),
		}, deliveryRejected, true},
		{"greylisted, still queued", greylisted("1", 0)[:4], deliveryDeferred, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subs := groupSubmissions(tc.events)
			if len(subs) != 1 {
				t.Fatalf("submissions = %d", len(subs))
			}
			out := subs[0].outcome()
			if out.Status != tc.status || out.Final != tc.final {
				t.Fatalf("outcome = %s final=%v, want %s final=%v", out.Status, out.Final, tc.status, tc.final)
			}
		})
	}
}

func TestOutcomeDetail(t *testing.T) {
	sub := groupSubmissions(subBounced("1", mailT0))[0]
	if got := outcomeDetail(sub, sub.outcome()); got != "550 5.4.1 Recipient address rejected: Access denied" {
		t.Fatalf("bounce detail = %q", got)
	}
	sub = groupSubmissions(subDelivered("1", mailT0))[0]
	if got := outcomeDetail(sub, sub.outcome()); got != "250 2.0.0 Ok: queued" {
		t.Fatalf("delivery detail = %q", got)
	}
	long := make([]byte, 0, 600)
	for len(long) < 600 {
		long = append(long, "ø"...)
	}
	if got := truncateUTF8(string(long), maxOutcomeDetail); len(got) > maxOutcomeDetail || got[len(got)-2:] != "ø" {
		t.Fatalf("truncate cut mid-rune: %d bytes, tail %q", len(got), got[len(got)-2:])
	}
}

func TestPairSweepRowsInOrder(t *testing.T) {
	t0 := mailT0
	// created_at is stamped before the relay is dialled, so B's row lands
	// just after A's submission. Closest-pair-first would swap them.
	rows := []sweepRow{
		{id: "a", createdAt: t0},
		{id: "b", createdAt: t0.Add(3 * time.Second)},
	}
	events := append(append(
		// A board reply from a shared mailbox, well after both.
		subDelivered("9", t0.Add(10*time.Minute)),
		subBounced("1", t0.Add(2*time.Second))...),
		subDelivered("2", t0.Add(4*time.Second))...)
	got := pairSweepRows(rows, groupSubmissions(events))
	if len(got) != 2 || got[0].QueueID != "1" || got[1].QueueID != "2" {
		t.Fatalf("pairs = %v", got)
	}
	// A row with no submission in its window stays unmatched, and so does
	// the foreign submission.
	got = pairSweepRows([]sweepRow{{id: "c", createdAt: t0.Add(time.Hour)}}, groupSubmissions(events))
	if len(got) != 0 {
		t.Fatalf("unexpected pair: %v", got)
	}
}

// sweepEnv is a database with a club and a sweep over a fake journal.
type sweepEnv struct {
	*systemActorEnv
	journal *sweepJournal
	sweep   *JournalSweep
	reader  *sdkmetric.ManualReader
}

func newSweepEnv(t *testing.T) *sweepEnv {
	t.Helper()
	e := newSystemActorEnv(t)
	j := &sweepJournal{events: map[string][]mailEvent{}}
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	return &sweepEnv{systemActorEnv: e, journal: j, reader: reader,
		sweep: newJournalSweep(e.db, j, zerolog.Nop(), mp.Meter("brygge-api"))}
}

// seedSweepSend inserts an email_sends row; checked is outcome_checked_at
// (nil for never).
func (e *sweepEnv) seedSend(t *testing.T, provider, recipient, status string, created time.Time, checked *time.Time) string {
	t.Helper()
	var id string
	if err := e.db.QueryRow(context.Background(),
		`INSERT INTO email_sends (club_id, kind, recipient, recipient_domain, provider, status, created_at, outcome_checked_at)
		 VALUES ($1, 'invoice', $2, split_part($2, '@', 2), $3, $4, $5, $6) RETURNING id`,
		e.clubID, recipient, provider, status, created, checked,
	).Scan(&id); err != nil {
		t.Fatalf("insert email send: %v", err)
	}
	return id
}

type sweptSend struct {
	status, detail string
	final, checked bool
	checkedAt      *time.Time
	lastEventAtSet bool
}

func (e *sweepEnv) read(t *testing.T, id string) sweptSend {
	t.Helper()
	var s sweptSend
	var detail *string
	if err := e.db.QueryRow(context.Background(),
		`SELECT status, outcome_detail, outcome_final_at IS NOT NULL, outcome_checked_at, last_event_at IS NOT NULL
		   FROM email_sends WHERE id = $1`, id,
	).Scan(&s.status, &detail, &s.final, &s.checkedAt, &s.lastEventAtSet); err != nil {
		t.Fatalf("read email send: %v", err)
	}
	if detail != nil {
		s.detail = *detail
	}
	s.checked = s.checkedAt != nil
	return s
}

func ago(d time.Duration) time.Time { return time.Now().Add(-d).UTC().Truncate(time.Millisecond) }

func TestIntegration_JournalSweepWritesOutcomes(t *testing.T) {
	t.Parallel()
	e := newSweepEnv(t)
	ctx := context.Background()

	t.Run("bounce writes rejected with detail, final", func(t *testing.T) {
		rcpt := "bounce@example.org"
		created := ago(30 * time.Minute)
		id := e.seedSend(t, "stalwart", rcpt, "submitted", created, nil)
		e.journal.events[rcpt] = subBounced("10", created.Add(time.Second))
		if err := e.sweep.sweepRecipient(ctx, rcpt); err != nil {
			t.Fatal(err)
		}
		got := e.read(t, id)
		if got.status != deliveryRejected || got.detail != "550 5.4.1 Recipient address rejected: Access denied" ||
			!got.final || !got.checked || !got.lastEventAtSet {
			t.Fatalf("row = %+v", got)
		}
		if n := stalwartOutcomeCount(t, e.reader, "rejected"); n != 1 {
			t.Fatalf("stalwart_outcome{result=rejected} = %d, want 1", n)
		}
	})

	t.Run("delivery writes accepted", func(t *testing.T) {
		rcpt := "ok@example.org"
		created := ago(30 * time.Minute)
		id := e.seedSend(t, "stalwart", rcpt, "submitted", created, nil)
		e.journal.events[rcpt] = subDelivered("20", created.Add(time.Second))
		if err := e.sweep.sweepRecipient(ctx, rcpt); err != nil {
			t.Fatal(err)
		}
		if got := e.read(t, id); got.status != deliveryAccepted || !got.final || got.detail != "250 2.0.0 Ok: queued" {
			t.Fatalf("row = %+v", got)
		}
	})

	t.Run("deferred, then accepted on the next run", func(t *testing.T) {
		rcpt := "grey@example.org"
		created := ago(30 * time.Minute)
		id := e.seedSend(t, "stalwart", rcpt, "submitted", created, nil)
		sub := created.Add(time.Second)
		all := []mailEvent{
			evAt(sub, mailEvQueued, "span", "in30", "queue", "30"),
			evAt(sub.Add(time.Second), mailEvAttemptStarted, "span", "a30", "queue", "30"),
			evAt(sub.Add(2*time.Second), mailEvRcptRejected, "span", "a30", "code", "450", "details", "4.7.1 greylisted"),
			evAt(sub.Add(5*time.Minute), mailEvAttemptStarted, "span", "b30", "queue", "30"),
			evAt(sub.Add(5*time.Minute+time.Second), mailEvDelivered, "span", "b30", "code", "250", "details", "2.0.0 Ok"),
		}
		e.journal.events[rcpt] = all[:3]
		if err := e.sweep.sweepRecipient(ctx, rcpt); err != nil {
			t.Fatal(err)
		}
		if got := e.read(t, id); got.status != deliveryDeferred || got.final || got.detail != "450 4.7.1 greylisted" {
			t.Fatalf("first run row = %+v", got)
		}
		e.backdateCheck(t, id)
		e.journal.events[rcpt] = all
		if err := e.sweep.sweepRecipient(ctx, rcpt); err != nil {
			t.Fatal(err)
		}
		if got := e.read(t, id); got.status != deliveryAccepted || !got.final {
			t.Fatalf("second run row = %+v", got)
		}
	})

	t.Run("5xx attempt then a later delivery moves rejected to accepted", func(t *testing.T) {
		rcpt := "twomx@example.org"
		created := ago(30 * time.Minute)
		id := e.seedSend(t, "stalwart", rcpt, "submitted", created, nil)
		first := subBounced("40", created.Add(time.Second))[:3]
		e.journal.events[rcpt] = first
		if err := e.sweep.sweepRecipient(ctx, rcpt); err != nil {
			t.Fatal(err)
		}
		if got := e.read(t, id); got.status != deliveryRejected || got.final {
			t.Fatalf("first run row = %+v", got)
		}
		e.backdateCheck(t, id)
		e.journal.events[rcpt] = append(first,
			evAt(created.Add(time.Minute), mailEvAttemptStarted, "span", "b40", "queue", "40"),
			evAt(created.Add(time.Minute+time.Second), mailEvDelivered, "span", "b40", "code", "250"))
		if err := e.sweep.sweepRecipient(ctx, rcpt); err != nil {
			t.Fatal(err)
		}
		if got := e.read(t, id); got.status != deliveryAccepted || !got.final {
			t.Fatalf("second run row = %+v", got)
		}
	})

	t.Run("queue expiry after 4xx is rejected_temporary", func(t *testing.T) {
		rcpt := "expired@example.org"
		created := ago(6 * 24 * time.Hour)
		id := e.seedSend(t, "stalwart", rcpt, "deferred", created, nil)
		sub := created.Add(time.Second)
		e.journal.events[rcpt] = []mailEvent{
			evAt(sub, mailEvQueued, "span", "in50", "queue", "50"),
			evAt(sub.Add(time.Second), mailEvAttemptStarted, "span", "a50", "queue", "50"),
			evAt(sub.Add(2*time.Second), mailEvRcptRejected, "span", "a50", "code", "451", "details", "4.3.0 mailbox busy"),
			evAt(sub.Add(5*24*time.Hour), mailEvFailed, "span", "a50", "queue", "50"),
		}
		if err := e.sweep.sweepRecipient(ctx, rcpt); err != nil {
			t.Fatal(err)
		}
		if got := e.read(t, id); got.status != deliveryRejectedTemporary || !got.final || got.detail != "451 4.3.0 mailbox busy" {
			t.Fatalf("row = %+v", got)
		}
	})

	t.Run("back-to-back sends pair in order; a foreign submission stays unclaimed", func(t *testing.T) {
		rcpt := "twice@example.org"
		created := ago(40 * time.Minute)
		a := e.seedSend(t, "stalwart", rcpt, "submitted", created, nil)
		b := e.seedSend(t, "stalwart", rcpt, "submitted", created.Add(3*time.Second), nil)
		e.journal.events[rcpt] = append(append(
			subBounced("61", created.Add(2*time.Second)),
			subDelivered("62", created.Add(4*time.Second))...),
			subBounced("69", created.Add(20*time.Minute))...) // a board reply
		if err := e.sweep.sweepRecipient(ctx, rcpt); err != nil {
			t.Fatal(err)
		}
		if ga, gb := e.read(t, a), e.read(t, b); ga.status != deliveryRejected || gb.status != deliveryAccepted {
			t.Fatalf("a = %s, b = %s; want rejected, accepted", ga.status, gb.status)
		}
	})

	t.Run("compare-and-set leaves a row that changed meanwhile", func(t *testing.T) {
		rcpt := "raced@example.org"
		created := ago(30 * time.Minute)
		id := e.seedSend(t, "stalwart", rcpt, "submitted", created, nil)
		e.journal.events[rcpt] = subBounced("70", created.Add(time.Second))
		e.journal.during = func() {
			if _, err := e.db.Exec(ctx, `UPDATE email_sends SET status = 'queued' WHERE id = $1`, id); err != nil {
				t.Errorf("concurrent write: %v", err)
			}
		}
		defer func() { e.journal.during = nil }()
		if err := e.sweep.sweepRecipient(ctx, rcpt); err != nil {
			t.Fatal(err)
		}
		if got := e.read(t, id); got.status != deliveryQueued || got.detail != "" || got.final || got.checked {
			t.Fatalf("row = %+v; want untouched apart from the concurrent write", got)
		}
	})

	t.Run("unmatched rows are stamped, then dropped after an hour", func(t *testing.T) {
		rcpt := "nomatch@example.org"
		fresh := e.seedSend(t, "stalwart", rcpt, "submitted", ago(30*time.Minute), nil)
		checkedAt := ago(50 * time.Minute)
		old := e.seedSend(t, "stalwart", rcpt, "submitted", ago(2*time.Hour), &checkedAt)
		if err := e.sweep.sweepRecipient(ctx, rcpt); err != nil {
			t.Fatal(err)
		}
		if got := e.read(t, fresh); !got.checked || got.status != deliverySubmitted {
			t.Fatalf("fresh row = %+v; want stamped", got)
		}
		if got := e.read(t, old); got.checkedAt == nil || !got.checkedAt.Equal(checkedAt) {
			t.Fatalf("old row checked_at = %v; want left at %v", got.checkedAt, checkedAt)
		}
	})

	t.Run("a helper error leaves rows untouched", func(t *testing.T) {
		rcpt := "helperdown@example.org"
		id := e.seedSend(t, "stalwart", rcpt, "submitted", ago(30*time.Minute), nil)
		e.journal.err = errors.New("helper down")
		defer func() { e.journal.err = nil }()
		if err := e.sweep.sweepRecipient(ctx, rcpt); err == nil {
			t.Fatal("helper error not reported")
		}
		if got := e.read(t, id); got.checked || got.status != deliverySubmitted {
			t.Fatalf("row = %+v; want untouched", got)
		}
	})

	t.Run("a failed row never takes the next send's submission", func(t *testing.T) {
		rcpt := "failedfirst@example.org"
		t0 := ago(time.Hour)
		failed := e.seedSend(t, "stalwart", rcpt, "failed", t0, nil)
		next := e.seedSend(t, "stalwart", rcpt, "submitted", t0.Add(30*time.Second), nil)
		e.journal.events[rcpt] = subBounced("75", t0.Add(31*time.Second))
		if err := e.sweep.sweepRecipient(ctx, rcpt); err != nil {
			t.Fatal(err)
		}
		if got := e.read(t, next); got.status != deliveryRejected {
			t.Fatalf("second row = %+v; want rejected", got)
		}
		if got := e.read(t, failed); got.status != deliveryFailed || got.checked {
			t.Fatalf("failed row = %+v; want untouched", got)
		}
	})

	t.Run("an unchanged status is not counted again", func(t *testing.T) {
		rcpt := "stilldeferred@example.org"
		created := ago(30 * time.Minute)
		id := e.seedSend(t, "stalwart", rcpt, "deferred", created, nil)
		e.journal.events[rcpt] = greylistedAt("76", created.Add(time.Second))
		before := stalwartOutcomeCount(t, e.reader, "deferred")
		if err := e.sweep.sweepRecipient(ctx, rcpt); err != nil {
			t.Fatal(err)
		}
		if got := e.read(t, id); got.status != deliveryDeferred || !got.checked || got.detail != "450 4.7.1 greylisted" {
			t.Fatalf("row = %+v", got)
		}
		if after := stalwartOutcomeCount(t, e.reader, "deferred"); after != before {
			t.Fatalf("stalwart_outcome{result=deferred} = %d, want unchanged %d", after, before)
		}
	})

	t.Run("queued is not written over deferred or accepted", func(t *testing.T) {
		for i, status := range []string{"deferred", "accepted"} {
			rcpt := "keep" + status + "@example.org"
			created := ago(30 * time.Minute)
			id := e.seedSend(t, "stalwart", rcpt, status, created, nil)
			if _, err := e.db.Exec(ctx,
				`UPDATE email_sends SET outcome_detail = 'earlier answer', last_event_at = $2 WHERE id = $1`,
				id, created.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			q := strconv.Itoa(77 + i)
			e.journal.events[rcpt] = []mailEvent{evAt(created.Add(time.Second), mailEvQueued, "span", "in"+q, "queue", q)}
			if err := e.sweep.sweepRecipient(ctx, rcpt); err != nil {
				t.Fatal(err)
			}
			if got := e.read(t, id); got.status != status || got.detail != "earlier answer" || !got.checked {
				t.Fatalf("%s row = %+v; want kept, stamped", status, got)
			}
		}
	})

	t.Run("an SES row is never touched", func(t *testing.T) {
		rcpt := "ses@example.org"
		created := ago(30 * time.Minute)
		id := e.seedSend(t, "ses", rcpt, "submitted", created, nil)
		e.journal.events[rcpt] = subBounced("80", created.Add(time.Second))
		if err := e.sweep.sweepRecipient(ctx, rcpt); err != nil {
			t.Fatal(err)
		}
		if got := e.read(t, id); got.checked || got.status != deliverySubmitted {
			t.Fatalf("SES row = %+v; want untouched", got)
		}
	})
}

// backdateCheck moves a row's last check past its back-off, as if the next
// run came later.
func (e *sweepEnv) backdateCheck(t *testing.T, id string) {
	t.Helper()
	if _, err := e.db.Exec(context.Background(),
		`UPDATE email_sends SET outcome_checked_at = now() - interval '2 hours' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

func TestIntegration_JournalSweepRunCapAndOrder(t *testing.T) {
	t.Parallel()
	e := newSweepEnv(t)
	ctx := context.Background()

	// Seven recipients with candidates; the run takes five: never checked
	// (oldest first), then least recently checked.
	checked := func(d time.Duration) *time.Time { at := ago(d); return &at }
	e.seedSend(t, "stalwart", "c1@example.org", "queued", ago(3*time.Hour), checked(2*time.Hour))
	e.seedSend(t, "stalwart", "c2@example.org", "queued", ago(5*time.Hour), checked(90*time.Minute))
	e.seedSend(t, "stalwart", "n1@example.org", "submitted", ago(20*time.Minute), nil)
	e.seedSend(t, "stalwart", "n2@example.org", "submitted", ago(40*time.Minute), nil)
	e.seedSend(t, "stalwart", "n3@example.org", "submitted", ago(10*time.Minute), nil)
	e.seedSend(t, "stalwart", "c3@example.org", "queued", ago(4*time.Hour), checked(70*time.Minute))
	// Not candidates: in back-off, too fresh, SES, final, too old, failed.
	e.seedSend(t, "stalwart", "backoff@example.org", "queued", ago(20*time.Minute), checked(time.Minute))
	e.seedSend(t, "stalwart", "fresh@example.org", "submitted", ago(time.Minute), nil)
	e.seedSend(t, "ses", "ses@example.org", "submitted", ago(time.Hour), nil)
	final := e.seedSend(t, "stalwart", "final@example.org", "accepted", ago(time.Hour), nil)
	if _, err := e.db.Exec(ctx, `UPDATE email_sends SET outcome_final_at = now() WHERE id = $1`, final); err != nil {
		t.Fatal(err)
	}
	e.seedSend(t, "stalwart", "old@example.org", "queued", ago(8*24*time.Hour), nil)
	e.seedSend(t, "stalwart", "failed@example.org", "failed", ago(time.Hour), nil)

	if err := e.sweep.runOnce(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"n2@example.org", "n1@example.org", "n3@example.org", "c1@example.org", "c2@example.org"}
	if len(e.journal.asked) != len(want) {
		t.Fatalf("asked = %v, want %v", e.journal.asked, want)
	}
	for i := range want {
		if e.journal.asked[i] != want[i] {
			t.Fatalf("asked = %v, want %v", e.journal.asked, want)
		}
	}
}

func TestIntegration_JournalSweepStopsOnCancel(t *testing.T) {
	t.Parallel()
	e := newSweepEnv(t)
	e.seedSend(t, "stalwart", "first@example.org", "submitted", ago(40*time.Minute), nil)
	e.seedSend(t, "stalwart", "second@example.org", "submitted", ago(30*time.Minute), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.journal.during = cancel
	if err := e.sweep.runOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("runOnce = %v, want context.Canceled", err)
	}
	if len(e.journal.asked) != 1 {
		t.Fatalf("asked = %v, want one recipient before the cancel", e.journal.asked)
	}
}

func stalwartOutcomeCount(t *testing.T, reader *sdkmetric.ManualReader, result string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	var n int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "brygge.email.stalwart_outcome" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				if v, ok := dp.Attributes.Value(attribute.Key("result")); ok && v.AsString() == result {
					dc, _ := dp.Attributes.Value(attribute.Key("recipient_domain_class"))
					kind, _ := dp.Attributes.Value(attribute.Key("kind"))
					if dc.AsString() != "other" || kind.AsString() != "invoice" {
						t.Errorf("attributes = %v", dp.Attributes)
					}
					n += dp.Value
				}
			}
		}
	}
	return n
}

func TestNewJournalSweepNeedsSocket(t *testing.T) {
	if s := newJournalSweep(nil, &sweepJournal{}, zerolog.Nop(), noop.NewMeterProvider().Meter("")); s == nil {
		t.Fatal("sweep with a journal is nil")
	}
	if NewJournalSweep(nil, nil, zerolog.Nop()) != nil {
		t.Fatal("sweep without a mail-log socket is not nil")
	}
}
