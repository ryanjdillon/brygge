package handlers

import (
	"context"
	"time"
)

// reminderJob is one bulk-reminder enqueueing — the worker pulls these
// off the queue, throttle-paced, and calls sendOneReminder for each.
//
// Context is deliberately NOT carried on the job: the handler's request
// context dies when the response returns, but the worker may take
// minutes to drain a large batch. Each job gets a fresh
// context.Background() with its own timeout inside runReminderWorker.
type reminderJob struct {
	clubID      string
	actorID     string
	remoteAddr  string
	invoiceID   string
	clubName    string
	defaultBank string
	locale      string
}

// startReminderWorker boots the single goroutine that drains the
// reminder queue. Called once from NewInvoiceHandler. The worker stops
// when the channel is closed — currently only at process shutdown.
//
// Phase 1 invariant: the queue is in-process and not persisted. On
// API restart, pending jobs are lost. See docs/developer/reference/invariants.md.
func (h *InvoiceHandler) startReminderWorker() {
	go func() {
		for job := range h.reminderQueue {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := h.sendOneReminder(ctx, job.clubID, job.actorID, job.remoteAddr,
				job.invoiceID, job.clubName, job.defaultBank, job.locale)
			cancel()
			if err != nil {
				h.log.Warn().Err(err).
					Str("invoice_id", job.invoiceID).
					Msg("async reminder send failed")
			}
			if h.config.BulkSendThrottle > 0 {
				time.Sleep(h.config.BulkSendThrottle)
			}
		}
	}()
}
