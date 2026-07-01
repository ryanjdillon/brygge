package broadcast

import (
	"context"
	"time"

	"github.com/rs/zerolog"
)

// OutgoingMessage is one rendered broadcast message bound for a single
// recipient.
type OutgoingMessage struct {
	To              string
	Subject         string
	BodyText        string
	BodyHTML        string
	AttachBodyParts []map[string]any
	// UserID is the recipient's member id (nil for ad-hoc addresses), so
	// the sender can attach a per-recipient unsubscribe link.
	UserID *string
}

// MessageSender delivers one broadcast message as the shared mailbox
// identified by sourceAddress. Implemented by the inbox handler
// (sendAsPrincipal); wrapped by BroadcastSender so the worker depends only
// on the generic Sender interface.
type MessageSender interface {
	SendBroadcast(ctx context.Context, sourceAddress string, msg OutgoingMessage) error
}

// Sender delivers a single claimed delivery of one kind. The worker owns
// the generic queue mechanics (claim, throttle, retry, mark); each Sender
// owns the kind-specific "how to actually send this row" — loading whatever
// parent content it needs and calling the right backend (JMAP for
// broadcasts, SMTP for invoice reminders).
type Sender interface {
	Send(ctx context.Context, c Claimed) error
}

// Worker drains the shared delivery queue: it claims pending deliveries of
// any kind, dispatches each to the Sender registered for its kind,
// throttles between sends to protect the mail backend, retries transient
// failures up to a cap, and (for broadcasts) rolls the parent up to
// 'complete' once all of its deliveries are resolved.
//
// It runs on a background context (not a request context), which is the
// fix for the old fire-and-forget truncation bug — sends no longer stop
// when an HTTP response returns. The queue is durable, so a restart
// resumes where it left off.
type Worker struct {
	store    *Store
	senders  map[string]Sender
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

// NewWorker builds a delivery worker with no senders yet — register one per
// kind via RegisterSender. throttle is the pause between individual sends
// (cfg.BulkSendThrottle).
func NewWorker(store *Store, throttle time.Duration, log zerolog.Logger) *Worker {
	return &Worker{
		store:       store,
		senders:     make(map[string]Sender),
		throttle:    throttle,
		log:         log.With().Str("component", "delivery-worker").Logger(),
		kick:        make(chan struct{}, 1),
		MaxAttempts: 3,
		BatchSize:   100,
	}
}

// RegisterSender binds a Sender to a delivery kind. Called at wiring time —
// the broadcast sender only when the inbox/JMAP stack is up, the invoice
// sender only when SMTP is configured — so a kind's rows are never created
// without its sender present.
func (w *Worker) RegisterSender(kind string, s Sender) {
	w.senders[kind] = s
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

		// Only broadcast-kind deliveries roll up a parent; collect the
		// touched broadcast ids to finalize after the batch.
		touched := map[string]struct{}{}
		for _, c := range claimed {
			if ctx.Err() != nil {
				return
			}
			w.process(ctx, c)
			if c.Kind == KindBroadcast && c.BroadcastID != nil {
				touched[*c.BroadcastID] = struct{}{}
			}
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

// process dispatches a single delivery to its kind's Sender and records the
// outcome. A failure short of the attempt cap returns the row to 'pending'
// for another round; at the cap it becomes terminally 'failed'. A delivery
// whose kind has no registered sender is left as-is (still 'sending') and
// reclaimed by the next boot sweep — this shouldn't happen since a kind's
// rows are only created when its sender exists.
func (w *Worker) process(ctx context.Context, c Claimed) {
	s, ok := w.senders[c.Kind]
	if !ok {
		w.log.Warn().Str("kind", c.Kind).Str("delivery", c.DeliveryID).
			Msg("no sender registered for kind; leaving delivery unclaimed")
		return
	}

	err := s.Send(ctx, c)
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
	w.log.Warn().Err(err).Str("kind", c.Kind).Str("to", c.Email).
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
