package broadcast

import (
	"context"
	"time"

	"github.com/rs/zerolog"
)

// OutgoingMessage is one rendered broadcast message bound for a single
// recipient.
type OutgoingMessage struct {
	To       string
	Subject  string
	BodyText string
	BodyHTML string
}

// MessageSender delivers one broadcast message as the shared mailbox
// identified by sourceAddress. Implemented by the inbox handler
// (sendAsPrincipal); the worker depends only on this interface so it can
// be exercised with a fake in tests.
type MessageSender interface {
	SendBroadcast(ctx context.Context, sourceAddress string, msg OutgoingMessage) error
}

// Worker drains the broadcast delivery queue: it claims pending deliveries,
// sends each as an individual (non-BCC) message through MessageSender,
// throttles between sends to protect the mail backend, retries transient
// failures up to a cap, and rolls a broadcast up to 'complete' once all of
// its deliveries are resolved.
//
// It runs on a background context (not a request context), which is the
// fix for the old fire-and-forget truncation bug — sends no longer stop
// when an HTTP response returns. The queue is durable, so a restart
// resumes where it left off.
type Worker struct {
	store    *Store
	sender   MessageSender
	throttle time.Duration
	log      zerolog.Logger
	kick     chan struct{}

	// MaxAttempts is the per-recipient send cap before a delivery is
	// marked terminally failed. BatchSize bounds how many rows are
	// claimed per round. Both have sensible defaults from NewWorker and
	// may be overridden before Run (e.g. in tests).
	MaxAttempts int
	BatchSize   int
}

// NewWorker builds a delivery worker. throttle is the pause between
// individual sends (cfg.BulkSendThrottle).
func NewWorker(store *Store, sender MessageSender, throttle time.Duration, log zerolog.Logger) *Worker {
	return &Worker{
		store:       store,
		sender:      sender,
		throttle:    throttle,
		log:         log.With().Str("component", "broadcast-worker").Logger(),
		kick:        make(chan struct{}, 1),
		MaxAttempts: 3,
		BatchSize:   100,
	}
}

// Kick nudges the worker to drain now instead of waiting for the next tick.
// Non-blocking: a kick already pending is enough.
func (w *Worker) Kick() {
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// Run starts the worker loop until ctx is cancelled. It first re-claims any
// deliveries left 'sending' by a previous crash (boot sweep), then drains
// on every tick and on every Kick.
func (w *Worker) Run(ctx context.Context, tick time.Duration) {
	if n, err := w.store.ResetOrphanedSending(ctx); err != nil {
		w.log.Error().Err(err).Msg("boot sweep failed")
	} else if n > 0 {
		w.log.Info().Int64("reclaimed", n).Msg("boot sweep reclaimed orphaned deliveries")
	}

	w.drain(ctx)

	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.drain(ctx)
		case <-w.kick:
			w.drain(ctx)
		}
	}
}

// drain processes claimed batches until the queue is empty (or ctx ends).
func (w *Worker) drain(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		claimed, err := w.store.ClaimPending(ctx, w.BatchSize)
		if err != nil {
			w.log.Error().Err(err).Msg("claim pending failed")
			return
		}
		if len(claimed) == 0 {
			return
		}

		touched := map[string]struct{}{}
		for _, c := range claimed {
			if ctx.Err() != nil {
				return
			}
			w.process(ctx, c)
			touched[c.BroadcastID] = struct{}{}
			if !w.sleep(ctx) {
				return
			}
		}
		for bid := range touched {
			if err := w.store.FinalizeIfDone(ctx, bid); err != nil {
				w.log.Error().Err(err).Str("broadcast_id", bid).Msg("finalize failed")
			}
		}
	}
}

// process sends a single delivery and records the outcome. A failure short
// of the attempt cap returns the row to 'pending' for another round; at the
// cap it becomes terminally 'failed'.
func (w *Worker) process(ctx context.Context, c Claimed) {
	err := w.sender.SendBroadcast(ctx, c.SourceAddress, OutgoingMessage{
		To:       c.Email,
		Subject:  c.Subject,
		BodyText: c.BodyText,
		BodyHTML: c.BodyHTML,
	})
	if err == nil {
		if merr := w.store.Mark(ctx, c.DeliveryID, DeliverySent, ""); merr != nil {
			w.log.Error().Err(merr).Str("delivery", c.DeliveryID).Msg("mark sent failed")
		}
		return
	}

	// c.Attempts is the count before this attempt; Mark increments it.
	next := DeliveryPending
	if c.Attempts+1 >= w.MaxAttempts {
		next = DeliveryFailed
	}
	if merr := w.store.Mark(ctx, c.DeliveryID, next, err.Error()); merr != nil {
		w.log.Error().Err(merr).Str("delivery", c.DeliveryID).Msg("mark failed")
	}
	w.log.Warn().Err(err).Str("to", c.Email).Str("broadcast_id", c.BroadcastID).
		Str("next", next).Int("attempt", c.Attempts+1).Msg("delivery attempt failed")
}

// sleep waits out the throttle, returning false if ctx is cancelled.
func (w *Worker) sleep(ctx context.Context) bool {
	if w.throttle <= 0 {
		return ctx.Err() == nil
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(w.throttle):
		return true
	}
}
