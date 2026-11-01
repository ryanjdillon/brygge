package accounting

import (
	"context"
	"sync"
)

// clubSlot lets one holder per club run at a time within the process. It
// backs the Stripe ingest and booking slot and the Vipps booking slot. A
// holder calls out or books for minutes at most and holds no database lock
// meanwhile, so the slot is in memory rather than in Postgres. It is
// per process: correctness never depends on it, every booking locks its own
// rows (see the accounting README, "Concurrency").
type clubSlot struct {
	mu    sync.Mutex
	slots map[string]chan struct{}
}

func (g *clubSlot) slot(clubID string) chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.slots == nil {
		g.slots = map[string]chan struct{}{}
	}
	ch, ok := g.slots[clubID]
	if !ok {
		ch = make(chan struct{}, 1)
		g.slots[clubID] = ch
	}
	return ch
}

// tryAcquire takes the club's slot if it is free.
func (g *clubSlot) tryAcquire(clubID string) (release func(), ok bool) {
	ch := g.slot(clubID)
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, true
	default:
		return nil, false
	}
}

// acquire waits for the club's slot until ctx ends.
func (g *clubSlot) acquire(ctx context.Context, clubID string) (release func(), err error) {
	ch := g.slot(clubID)
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
