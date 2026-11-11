package accounting

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Unit tests of StripeAutoBooker (BRY-570): the debounce, its cap, a kick
// during a run, and shutdown. A fake clock drives the timers.

type fakeWaiter struct {
	at time.Time
	ch chan time.Time
}

type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*fakeWaiter
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// After fires at until, at once when until is not after the clock.
func (c *fakeClock) After(until time.Time) (<-chan time.Time, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := &fakeWaiter{at: until, ch: make(chan time.Time, 1)}
	if !until.After(c.now) {
		w.ch <- c.now
		return w.ch, func() {}
	}
	c.waiters = append(c.waiters, w)
	return w.ch, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		for i, x := range c.waiters {
			if x == w {
				c.waiters = append(c.waiters[:i], c.waiters[i+1:]...)
				return
			}
		}
	}
}

// Advance moves the clock on and fires every timer that is due.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.at.After(c.now) {
			w.ch <- c.now
			continue
		}
		kept = append(kept, w)
	}
	c.waiters = kept
}

// waiterAt reports whether the one timer waiting is due at t.
func (c *fakeClock) waiterAt(t time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters) == 1 && c.waiters[0].at.Equal(t)
}

func (c *fakeClock) idle() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters) == 0
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// bookerHarness runs a booker on a fake clock and records each run's club
// and clock time. gate, when set, holds every run until it is closed;
// result, when set, is what each run returns.
type bookerHarness struct {
	result  func(n int) *StripeAutoBookingResult
	clock   *fakeClock
	booker  *StripeAutoBooker
	mu      sync.Mutex
	runs    []time.Time
	clubs   []string
	started chan struct{}
	gate    chan struct{}
	cancel  context.CancelFunc
	done    chan struct{}
}

func newBookerHarness(t *testing.T, gated bool) *bookerHarness {
	t.Helper()
	return newBookerHarnessWith(t, gated, nil)
}

func newBookerHarnessWith(t *testing.T, gated bool, result func(n int) *StripeAutoBookingResult) *bookerHarness {
	t.Helper()
	h := &bookerHarness{clock: newFakeClock(), started: make(chan struct{}, 64), done: make(chan struct{})}
	h.result = result
	if gated {
		h.gate = make(chan struct{})
	}
	h.booker = newStripeAutoBooker(func(ctx context.Context, clubID string) *StripeAutoBookingResult {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("run has no deadline")
		}
		h.mu.Lock()
		h.runs = append(h.runs, h.clock.Now())
		h.clubs = append(h.clubs, clubID)
		gate, result, n := h.gate, h.result, len(h.runs)
		h.mu.Unlock()
		h.started <- struct{}{}
		if gate != nil {
			<-gate
		}
		if result != nil {
			return result(n)
		}
		return &StripeAutoBookingResult{}
	}, h.clock.Now, h.clock.After)
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() {
		defer close(h.done)
		h.booker.Run(ctx)
	}()
	t.Cleanup(func() {
		h.mu.Lock()
		if h.gate != nil {
			select {
			case <-h.gate:
			default:
				close(h.gate)
			}
		}
		h.mu.Unlock()
		cancel()
		<-h.done
	})
	return h
}

func (h *bookerHarness) runCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.runs)
}

// kickAndWait kicks the club and waits until the booker waits for t.
func (h *bookerHarness) kickAndWait(t *testing.T, club string, due time.Time) {
	t.Helper()
	h.booker.Kick(club)
	eventually(t, "the booker's timer at "+due.Format(time.TimeOnly), func() bool { return h.clock.waiterAt(due) })
}

func TestStripeAutoBookerDebouncesKicks(t *testing.T) {
	h := newBookerHarness(t, false)
	t0 := h.clock.Now()

	h.kickAndWait(t, "club-a", t0.Add(30*time.Second))
	h.clock.Advance(10 * time.Second)
	h.kickAndWait(t, "club-a", t0.Add(40*time.Second))
	h.clock.Advance(10 * time.Second)
	h.kickAndWait(t, "club-a", t0.Add(50*time.Second))
	if n := h.runCount(); n != 0 {
		t.Fatalf("ran %d times before the debounce ended", n)
	}

	h.clock.Advance(30 * time.Second)
	eventually(t, "one run", func() bool { return h.runCount() == 1 })
	eventually(t, "the booker idle", h.clock.idle)
	h.clock.Advance(time.Hour)
	time.Sleep(20 * time.Millisecond)
	if n := h.runCount(); n != 1 {
		t.Fatalf("three kicks in 30 s ran %d times, want 1", n)
	}
	if h.clubs[0] != "club-a" || !h.runs[0].Equal(t0.Add(50*time.Second)) {
		t.Errorf("run = %s at %s", h.clubs[0], h.runs[0])
	}
}

func TestStripeAutoBookerCapsTheDelay(t *testing.T) {
	h := newBookerHarness(t, false)
	t0 := h.clock.Now()
	h.kickAndWait(t, "club-a", t0.Add(30*time.Second))
	// A kick every 20 s would push the run on for ever; the first kick + 2
	// min is the latest it runs.
	for i := 1; i <= 5; i++ {
		h.clock.Advance(20 * time.Second)
		due := t0.Add(time.Duration(20*i+30) * time.Second)
		if limit := t0.Add(2 * time.Minute); due.After(limit) {
			due = limit
		}
		h.kickAndWait(t, "club-a", due)
	}
	h.clock.Advance(20 * time.Second)
	eventually(t, "the capped run", func() bool { return h.runCount() == 1 })
	if !h.runs[0].Equal(t0.Add(2 * time.Minute)) {
		t.Errorf("ran at %s, want the first kick + 2 min", h.runs[0].Sub(t0))
	}
}

func TestStripeAutoBookerKickDuringRun(t *testing.T) {
	h := newBookerHarness(t, true)
	t0 := h.clock.Now()
	h.kickAndWait(t, "club-a", t0.Add(30*time.Second))
	h.clock.Advance(30 * time.Second)
	<-h.started

	// The run is held: kicks still return at once.
	kicked := make(chan struct{})
	go func() {
		for range 3 {
			h.booker.Kick("club-a")
		}
		close(kicked)
	}()
	select {
	case <-kicked:
	case <-time.After(2 * time.Second):
		t.Fatal("Kick blocked while a run was in progress")
	}

	h.mu.Lock()
	close(h.gate)
	h.gate = nil
	h.mu.Unlock()
	due := t0.Add(60 * time.Second)
	eventually(t, "the follow-up run's timer", func() bool { return h.clock.waiterAt(due) })
	h.clock.Advance(30 * time.Second)
	eventually(t, "the follow-up run", func() bool { return h.runCount() == 2 })
	eventually(t, "the booker idle", h.clock.idle)
	h.clock.Advance(time.Hour)
	time.Sleep(20 * time.Millisecond)
	if n := h.runCount(); n != 2 {
		t.Fatalf("runs = %d, want exactly one more after the kicks during the run", n)
	}
}

func TestStripeAutoBookerRunsClubsOneAtATime(t *testing.T) {
	h := newBookerHarness(t, false)
	t0 := h.clock.Now()
	h.kickAndWait(t, "club-a", t0.Add(30*time.Second))
	h.clock.Advance(5 * time.Second)
	h.booker.Kick("club-b")
	h.clock.Advance(time.Minute)
	eventually(t, "both runs", func() bool { return h.runCount() == 2 })
	if h.clubs[0] != "club-a" || h.clubs[1] != "club-b" {
		t.Errorf("clubs = %v, want a then b", h.clubs)
	}
}

func TestStripeAutoBookerStopsOnCancel(t *testing.T) {
	h := newBookerHarness(t, false)
	h.kickAndWait(t, "club-a", h.clock.Now().Add(30*time.Second))
	h.cancel()
	select {
	case <-h.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return when its context ended")
	}
	if n := h.runCount(); n != 0 {
		t.Errorf("runs = %d after cancel, want 0", n)
	}
	h.booker.Kick("club-a") // never blocks, also with no Run
}

// A kicked run that was busy, could not ingest or was cut short is kicked
// again, at most stripeKickMaxRetries times in a row; a run that got through
// resets the count.
func TestStripeAutoBookerRetriesDroppedRuns(t *testing.T) {
	// Runs 1-4 are busy (the first and its three retries), run 5 is a new
	// kick that books, run 6 fails its ingest and is retried once (run 7).
	h := newBookerHarnessWith(t, false, func(n int) *StripeAutoBookingResult {
		switch {
		case n <= 4:
			return &StripeAutoBookingResult{Busy: true}
		case n == 6:
			return &StripeAutoBookingResult{IngestFailed: true}
		}
		return &StripeAutoBookingResult{}
	})
	t0 := h.clock.Now()
	h.kickAndWait(t, "club-a", t0.Add(30*time.Second))
	for i := 1; i <= 4; i++ {
		h.clock.Advance(30 * time.Second)
		eventually(t, "run", func() bool { return h.runCount() == i })
		if i < 4 {
			eventually(t, "the retry's timer", func() bool { return h.clock.waiterAt(h.clock.Now().Add(30 * time.Second)) })
		}
	}
	eventually(t, "the booker idle after the retry cap", h.clock.idle)
	h.clock.Advance(time.Hour)
	time.Sleep(20 * time.Millisecond)
	if n := h.runCount(); n != 4 {
		t.Fatalf("runs = %d, want the first and three retries", n)
	}

	h.kickAndWait(t, "club-a", h.clock.Now().Add(30*time.Second))
	h.clock.Advance(30 * time.Second)
	eventually(t, "run 5", func() bool { return h.runCount() == 5 })
	eventually(t, "idle after a good run", h.clock.idle)

	h.kickAndWait(t, "club-a", h.clock.Now().Add(30*time.Second))
	h.clock.Advance(30 * time.Second)
	eventually(t, "run 6", func() bool { return h.runCount() == 6 })
	eventually(t, "run 6's retry timer", func() bool { return h.clock.waiterAt(h.clock.Now().Add(30 * time.Second)) })
	h.clock.Advance(30 * time.Second)
	eventually(t, "run 7", func() bool { return h.runCount() == 7 })
	eventually(t, "idle after the retry booked", h.clock.idle)
}

// An aborted run is retried only while the booker itself still runs: an
// abort because the API is shutting down is left to the next boot run.
func TestStripeAutoBookerNoRetryOnShutdown(t *testing.T) {
	clock := newFakeClock()
	b := newStripeAutoBooker(nil, clock.Now, clock.After)
	pending := func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		_, ok := b.pending["club-a"]
		return ok
	}
	b.afterRun(cancelledContext(), "club-a", &StripeAutoBookingResult{Aborted: true})
	if pending() || b.retries["club-a"] != 0 {
		t.Fatal("an abort at shutdown was retried")
	}
	b.afterRun(context.Background(), "club-a", &StripeAutoBookingResult{Aborted: true})
	if !pending() || b.retries["club-a"] != 1 {
		t.Fatal("an abort while running was not retried")
	}
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
