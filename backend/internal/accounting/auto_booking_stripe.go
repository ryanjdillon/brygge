package accounting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
)

// The automatic Stripe run (ACC-13, BRY-570). RunAutomaticStripeBooking is
// what cmd/api runs at every start of the API and every 24 h after, and what
// StripeAutoBooker runs shortly after a webhook reported a capture, refund,
// dispute fund movement or payout. It ingests the Stripe balance and, when
// the club has turned on clubs.automatic_booking, books what is ready and
// clears the payouts that are ready, as the club's system actor: the same
// passes a person's Stripe sync runs (TrySyncAndBookStripeBalance), in one
// hold of the club's Stripe slot. Booking always reads the balance, never a
// webhook's payload (ACC-5); a refund's credit note is issued and queued by
// the booking pass itself (afterRefundBooked).

const (
	// defaultStripeAutoSlotWait is how long the automatic Stripe run waits
	// for the club's Stripe slot; still held, it records itself busy. A
	// person's sync holding the slot books the same rows.
	defaultStripeAutoSlotWait = 2 * time.Minute
	// stripeKickDebounce is how long the booker waits after a webhook kick
	// before it runs, so a burst of events (a capture's session and intent
	// events, a refund's created and updated) is one run, and Stripe has
	// listed the event's balance transaction by then.
	stripeKickDebounce = 30 * time.Second
	// stripeKickMaxDelay caps how far repeated kicks push a club's run past
	// the first kick.
	stripeKickMaxDelay = 2 * time.Minute
	// stripeKickRunTimeout bounds one kicked run.
	stripeKickRunTimeout = 5 * time.Minute
)

// SetStripeAutoSlotWait sets how long the automatic Stripe run waits for the
// club's Stripe slot; d <= 0 restores the default. Tests shorten it.
func (s *Service) SetStripeAutoSlotWait(d time.Duration) {
	s.stripeAutoSlotWait = d
}

func (s *Service) stripeAutoWait() time.Duration {
	if s.stripeAutoSlotWait > 0 {
		return s.stripeAutoSlotWait
	}
	return defaultStripeAutoSlotWait
}

// StripeAutoBookingResult is what one automatic Stripe run did.
type StripeAutoBookingResult struct {
	Trigger string
	// Booked is true when the club's switch was on and the run tried to
	// ingest and book as the system actor.
	Booked bool
	// Skipped: a webhook-kicked run of a club with the switch off, which
	// does nothing (not even an ingest).
	Skipped bool
	// Busy: the club's Stripe slot stayed held past the run's wait, so
	// nothing was ingested or booked this time.
	Busy bool
	// Aborted: the run's context ended before it finished.
	Aborted bool
	// IngestFailed: the Stripe balance could not be listed, so nothing was
	// booked (a booking only follows a complete ingest).
	IngestFailed bool
	// Ingest is the balance ingest's result; nil when it did not run.
	Ingest *StripeSyncResult
	// Booking and Payouts are the booking pass and the payout sweep; nil
	// when they did not run.
	Booking *StripeBookResult
	Payouts *StripePayoutSweepResult
	// Errors are the steps that failed outside the passes' own results.
	Errors   []string
	Duration time.Duration
}

// Changed reports whether the run booked a row or cleared a payout.
func (r *StripeAutoBookingResult) Changed() bool {
	if b := r.Booking; b != nil && b.ChargesBooked+b.FeesBooked+b.RefundsBooked+b.DisputesBooked > 0 {
		return true
	}
	return r.Payouts != nil && len(r.Payouts.Cleared) > 0
}

// Failed reports whether a step of the run failed: the booking pass or the
// sweep, a row the pass could not book, or a step outside them (the switch,
// the system actor), or the run was aborted. A failed ingest is
// IngestFailed and Busy is Busy; neither is a failure here (the operator's
// last-run-failed metric stays narrow), but both make NeedsAudit true.
func (r *StripeAutoBookingResult) Failed() bool {
	if len(r.Errors) > 0 || r.Aborted {
		return true
	}
	if b := r.Booking; b != nil && (b.Error != "" || len(b.Errors) > 0) {
		return true
	}
	return r.Payouts != nil && r.Payouts.Error != ""
}

// NeedsAudit reports whether the run writes its summary audit row: it
// booked or cleared something, failed, was busy or aborted, could not
// ingest, or a payout was ready but failed to post.
func (r *StripeAutoBookingResult) NeedsAudit() bool {
	if r.Changed() || r.Failed() || r.Busy || r.IngestFailed {
		return true
	}
	return r.Payouts != nil && len(r.Payouts.Failed) > 0
}

// RunAutomaticStripeBooking is the automatic Stripe run for one club. With
// the club's automatic_booking switch off, a webhook-kicked run
// (AutoBookingTriggerStripeEvent) does nothing and a boot or nightly run
// only ingests, as the nightly job always did. With it on, the run ingests,
// books and sweeps as the club's system actor in one hold of the club's
// Stripe slot, waiting for the slot at most the run's slot wait (then
// Busy). The summary audit row (accounting.auto_booking_run, provider
// stripe) is written when NeedsAudit; the observer is told at the end of
// every run. The run is bounded by autoBookingDeadline.
func (s *Service) RunAutomaticStripeBooking(ctx context.Context, clubID string, src StripeBalanceSource, trigger string) *StripeAutoBookingResult { //nolint:cyclop // one ordered run; each step is a few lines
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, autoBookingDeadline)
	defer cancel()
	res := &StripeAutoBookingResult{Trigger: trigger}
	if src == nil {
		res.Errors = append(res.Errors, "stripe client not configured")
		s.finishStripeAutoBooking(clubID, res, start)
		return res
	}

	var on bool
	err := s.db.QueryRow(ctx, `SELECT automatic_booking FROM clubs WHERE id = $1`, clubID).Scan(&on)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = fmt.Errorf("club not found")
		}
		res.Aborted = ctx.Err() != nil
		// Whether the club wants booking is unknown: nothing is booked and
		// no audit row written; the failure reaches the operator.
		s.log.Error().Err(err).Str("club_id", clubID).Msg("automatic stripe booking: reading the club's switch failed")
		res.Errors = append(res.Errors, "reading the club's switch: "+err.Error())
		s.finishStripeAutoBooking(clubID, res, start)
		return res
	}

	if !on {
		if trigger == AutoBookingTriggerStripeEvent {
			res.Skipped = true
			s.finishStripeAutoBooking(clubID, res, start)
			return res
		}
		synced, err := s.SyncStripeBalance(ctx, src, clubID)
		res.Ingest = &synced
		switch {
		case err != nil && ctx.Err() != nil:
			res.Aborted = true
		case err != nil:
			res.IngestFailed = true
			s.log.Warn().Err(err).Str("club_id", clubID).Msg("stripe balance sync failed")
		}
		s.finishStripeAutoBooking(clubID, res, start)
		return res
	}

	actor, err := s.SystemActor(ctx, clubID)
	if err != nil {
		res.Errors = append(res.Errors, "resolving the system actor: "+err.Error())
		res.Aborted = ctx.Err() != nil
		s.writeStripeAutoBookingAudit(ctx, clubID, "", res, start)
		s.finishStripeAutoBooking(clubID, res, start)
		return res
	}
	res.Booked = true

	synced, booked, payouts, err := s.SyncAndBookStripeBalance(ctx, src, clubID, actor, s.stripeAutoWait())
	res.Ingest, res.Booking, res.Payouts = &synced, booked, payouts
	switch {
	case errors.Is(err, ErrStripeSyncBusy):
		res.Busy = true
		res.Ingest = nil
	case err != nil && ctx.Err() != nil:
		res.Aborted = true
	case err != nil:
		res.IngestFailed = true
		s.log.Warn().Err(err).Str("club_id", clubID).Msg("automatic stripe booking: ingest failed; nothing booked")
	case ctx.Err() != nil:
		// The passes ran to their end but the context ended during them:
		// rows they did not reach stay unbooked for the next run.
		res.Aborted = true
	}

	sig := stripeFailureSignature(res)
	prev := s.swapStripeAutoFailure(clubID, sig)
	// A kicked run that changed nothing and failed the same way as the
	// club's previous run (busy again, the same ingest error) writes no
	// second row: the previous row already says it. Boot and nightly runs
	// always write theirs, so a stuck state is still recorded every night.
	repeat := trigger == AutoBookingTriggerStripeEvent && !res.Changed() && sig != "" && sig == prev
	if res.NeedsAudit() && !repeat {
		s.writeStripeAutoBookingAudit(ctx, clubID, actor, res, start)
	}
	s.finishStripeAutoBooking(clubID, res, start)
	return res
}

// stripeFailureSignature is what a run failed on, for comparing it with the
// club's previous run; "" when it failed on nothing.
func stripeFailureSignature(res *StripeAutoBookingResult) string {
	parts := []string{}
	if res.Busy {
		parts = append(parts, "busy")
	}
	if res.IngestFailed {
		parts = append(parts, "ingest_failed")
	}
	if res.Aborted {
		parts = append(parts, "aborted")
	}
	parts = append(parts, res.Errors...)
	if b := res.Booking; b != nil {
		if b.Error != "" {
			parts = append(parts, "booking:"+b.Error)
		}
		parts = append(parts, b.Errors...)
	}
	if p := res.Payouts; p != nil {
		if p.Error != "" {
			parts = append(parts, "payouts:"+p.Error)
		}
		for _, f := range p.Failed {
			parts = append(parts, "payout:"+f.PayoutID+":"+f.Reason)
		}
	}
	return strings.Join(parts, "\n")
}

// swapStripeAutoFailure records the club's latest failure signature and
// returns the one before it.
func (s *Service) swapStripeAutoFailure(clubID, sig string) string {
	s.stripeAutoMu.Lock()
	defer s.stripeAutoMu.Unlock()
	if s.stripeAutoLastFailure == nil {
		s.stripeAutoLastFailure = map[string]string{}
	}
	prev := s.stripeAutoLastFailure[clubID]
	s.stripeAutoLastFailure[clubID] = sig
	return prev
}

// finishStripeAutoBooking tells the observer and logs the run's one line.
func (s *Service) finishStripeAutoBooking(clubID string, res *StripeAutoBookingResult, start time.Time) {
	res.Duration = time.Since(start)
	if s.autoBookingObs != nil {
		s.autoBookingObs.AutoBookingRun(AutoBookingProviderStripe, res.Failed())
	}
	ev := s.log.Info()
	if res.Failed() {
		ev = s.log.Warn()
	} else if res.Skipped {
		ev = s.log.Debug()
	}
	ev = ev.Str("club_id", clubID).Str("trigger", res.Trigger).Bool("booked", res.Booked).
		Bool("skipped", res.Skipped).Bool("busy", res.Busy).Bool("aborted", res.Aborted).
		Bool("ingest_failed", res.IngestFailed).Strs("errors", res.Errors).Dur("duration", res.Duration)
	if in := res.Ingest; in != nil {
		ev = ev.Int("fetched", in.Fetched).Int("inserted", in.Inserted).Int("payouts_seen", in.PayoutsSeen).
			Int("ingest_errors", len(in.Errors))
	}
	if b := res.Booking; b != nil {
		ev = ev.Int("charges_booked", b.ChargesBooked).Int("fees_booked", b.FeesBooked).
			Int("refunds_booked", b.RefundsBooked).Int("disputes_booked", b.DisputesBooked).
			Int("deferred", len(b.Deferred)).Int("booking_errors", len(b.Errors))
	}
	if p := res.Payouts; p != nil {
		ev = ev.Int("payouts_cleared", len(p.Cleared)).Int("payouts_failed", len(p.Failed))
	}
	ev.Msg("automatic stripe booking run")
	if in := res.Ingest; in != nil && len(in.Errors) > 0 {
		s.log.Warn().Strs("errors", in.Errors).Str("club_id", clubID).Msg("stripe balance sync: some rows failed")
	}
}

// writeStripeAutoBookingAudit writes the run's summary row on
// postCommitContext, so a run whose context ended still records itself.
func (s *Service) writeStripeAutoBookingAudit(ctx context.Context, clubID, actor string, res *StripeAutoBookingResult, start time.Time) {
	if s.audit == nil {
		return
	}
	res.Duration = time.Since(start)
	actx, cancel := postCommitContext(ctx)
	defer cancel()
	s.audit.LogAction(actx, clubID, actor, "", audit.ActionAccountingAutoBookingRun, "accounting", clubID, stripeAutoBookingAuditDetails(res))
}

// stripeAutoBookingAuditDetails is the summary row's details: snake_case,
// ids and counts only, lists capped at autoBookingAuditListCap.
func stripeAutoBookingAuditDetails(res *StripeAutoBookingResult) map[string]any {
	d := map[string]any{
		"provider":      AutoBookingProviderStripe,
		"trigger":       res.Trigger,
		"busy":          res.Busy,
		"aborted":       res.Aborted,
		"ingest_failed": res.IngestFailed,
		"duration_ms":   res.Duration.Milliseconds(),
	}
	capList(d, "errors", res.Errors)
	if in := res.Ingest; in != nil {
		d["ingest"] = map[string]any{
			"fetched": in.Fetched, "inserted": in.Inserted, "payouts_seen": in.PayoutsSeen, "errors": len(in.Errors),
		}
	}
	st := map[string]any{}
	if b := res.Booking; b != nil {
		st["charges_booked"] = b.ChargesBooked
		st["fees_booked"] = b.FeesBooked
		st["refunds_booked"] = b.RefundsBooked
		st["disputes_booked"] = b.DisputesBooked
		st["deferred"] = len(b.Deferred)
		st["needs_review"] = b.NeedsReview
		capList(st, "booking_errors", b.Errors)
		if b.Error != "" {
			st["booking_error"] = b.Error
		}
	}
	if p := res.Payouts; p != nil {
		cleared := make([]string, 0, len(p.Cleared))
		for _, c := range p.Cleared {
			cleared = append(cleared, c.PayoutID)
		}
		capList(st, "payouts_cleared", cleared)
		failed := make([]map[string]string, 0, len(p.Failed))
		for _, f := range p.Failed {
			failed = append(failed, map[string]string{"payout": f.PayoutID, "reason": f.Reason})
		}
		capList(st, "payouts_failed", failed)
		st["payouts_waiting"] = len(p.Waiting)
		st["payouts_skipped"] = len(p.Skipped)
		st["declined_skipped"] = p.DeclinedSkipped
		if p.Error != "" {
			st["payouts_error"] = p.Error
		}
	}
	d["stripe"] = st
	return d
}

// StripeAutoBooker runs the automatic Stripe run for a club shortly after a
// webhook reported something to book (BRY-570). Kick never blocks: it marks
// the club due stripeKickDebounce from now, and repeated kicks move that on
// but never past stripeKickMaxDelay after the first. One goroutine (Run)
// runs due clubs one at a time, each bounded by stripeKickRunTimeout. A kick
// that arrives while the club's run is in progress schedules exactly one
// more run. A run that did nothing because it was busy, could not ingest or
// was cut short is kicked again, at most stripeKickMaxRetries times in a row
// per club, so a lasting failure waits for the nightly run instead of
// looping.
type StripeAutoBooker struct {
	run      func(ctx context.Context, clubID string) *StripeAutoBookingResult
	now      func() time.Time
	after    func(until time.Time) (<-chan time.Time, func())
	log      zerolog.Logger
	debounce time.Duration
	maxDelay time.Duration
	timeout  time.Duration

	mu      sync.Mutex
	pending map[string]stripeKick
	wake    chan struct{}
	// retries counts, per club, the kicked runs in a row that asked to be
	// retried; only Run touches it.
	retries map[string]int
}

// stripeKickMaxRetries bounds how many times in a row a club's kicked run
// is retried after it was busy, could not ingest or was cut short.
const stripeKickMaxRetries = 3

type stripeKick struct {
	first, due time.Time
}

// NewStripeAutoBooker returns a booker that runs RunAutomaticStripeBooking
// against src with the stripe_event trigger. Start Run in a goroutine.
func NewStripeAutoBooker(svc *Service, src StripeBalanceSource, log zerolog.Logger) *StripeAutoBooker {
	log = log.With().Str("component", "stripe_auto_booker").Logger()
	b := newStripeAutoBooker(func(ctx context.Context, clubID string) *StripeAutoBookingResult {
		log.Debug().Str("club_id", clubID).Msg("running the automatic stripe booking a webhook kicked")
		return svc.RunAutomaticStripeBooking(ctx, clubID, src, AutoBookingTriggerStripeEvent)
	}, time.Now, realAfter)
	b.log = log
	return b
}

func newStripeAutoBooker(run func(context.Context, string) *StripeAutoBookingResult, now func() time.Time,
	after func(time.Time) (<-chan time.Time, func())) *StripeAutoBooker {
	return &StripeAutoBooker{
		run: run, now: now, after: after, log: zerolog.Nop(),
		debounce: stripeKickDebounce, maxDelay: stripeKickMaxDelay, timeout: stripeKickRunTimeout,
		pending: map[string]stripeKick{},
		wake:    make(chan struct{}, 1),
		retries: map[string]int{},
	}
}

// realAfter fires at until, at once when until has passed.
func realAfter(until time.Time) (<-chan time.Time, func()) {
	t := time.NewTimer(time.Until(until))
	return t.C, func() { t.Stop() }
}

// Kick asks for a run of the club's automatic Stripe booking soon. It never
// blocks and is safe from any goroutine; an empty club id is ignored.
func (b *StripeAutoBooker) Kick(clubID string) {
	if clubID == "" {
		return
	}
	now := b.now()
	b.mu.Lock()
	k, ok := b.pending[clubID]
	if !ok {
		k.first = now
	}
	k.due = now.Add(b.debounce)
	if limit := k.first.Add(b.maxDelay); k.due.After(limit) {
		k.due = limit
	}
	b.pending[clubID] = k
	b.mu.Unlock()
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// Run runs due clubs until ctx ends. Call it once, in its own goroutine.
func (b *StripeAutoBooker) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		club, until, due, pending := b.next()
		if due {
			rctx, cancel := context.WithTimeout(ctx, b.timeout)
			res := b.run(rctx, club)
			cancel()
			b.afterRun(ctx, club, res)
			continue
		}
		var timer <-chan time.Time
		stop := func() {}
		if pending {
			timer, stop = b.after(until)
		}
		select {
		case <-ctx.Done():
			stop()
			return
		case <-b.wake:
		case <-timer:
		}
		stop()
	}
}

// afterRun kicks the club again when its run did nothing it could have done
// (busy, no ingest, or cut short while the booker itself still runs), up to
// stripeKickMaxRetries times in a row; any other outcome resets the count.
func (b *StripeAutoBooker) afterRun(ctx context.Context, clubID string, res *StripeAutoBookingResult) {
	retry := res != nil && ctx.Err() == nil && (res.Busy || res.IngestFailed || res.Aborted)
	if !retry {
		delete(b.retries, clubID)
		return
	}
	if b.retries[clubID] >= stripeKickMaxRetries {
		delete(b.retries, clubID)
		b.log.Warn().Str("club_id", clubID).Int("retries", stripeKickMaxRetries).
			Msg("automatic stripe booking still not done after retries; the nightly run is the safety net")
		return
	}
	b.retries[clubID]++
	b.Kick(clubID)
}

// next takes the earliest due club off the queue. When none is due it
// reports when the earliest one is (pending false when none is waiting).
func (b *StripeAutoBooker) next() (clubID string, until time.Time, due, pending bool) {
	now := b.now()
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, k := range b.pending {
		if clubID == "" || k.due.Before(until) {
			clubID, until = id, k.due
		}
	}
	if clubID == "" {
		return "", time.Time{}, false, false
	}
	if !until.After(now) {
		delete(b.pending, clubID)
		return clubID, until, true, true
	}
	return "", until, false, true
}
