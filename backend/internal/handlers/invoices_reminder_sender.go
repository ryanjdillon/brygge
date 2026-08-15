package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
)

// invoiceReminderPayload is the send context carried on the durable
// delivery row. The request that enqueued the reminder is long gone by the
// time the worker sends, so everything sendOneReminder needs beyond the
// invoice id itself travels here in the row's JSONB payload.
type invoiceReminderPayload struct {
	Locale       string `json:"locale"`
	ClubName     string `json:"clubName"`
	DefaultBank  string `json:"defaultBank"`
	ActorID      string `json:"actorID"`
	RemoteAddr   string `json:"remoteAddr"`
	IncludeVipps bool   `json:"includeVipps"`
}

// InvoiceReminderSender is the broadcast.Sender for 'invoice_reminder'
// deliveries. It adapts the invoice handler's SMTP reminder send to the
// shared durable delivery worker so reminders get the same throttle,
// retry, and restart-survival as broadcasts.
type InvoiceReminderSender struct {
	h *InvoiceHandler
}

// NewInvoiceReminderSender binds the sender to the invoice handler (which
// owns the SMTP client, DB, and audit service).
func NewInvoiceReminderSender(h *InvoiceHandler) *InvoiceReminderSender {
	return &InvoiceReminderSender{h: h}
}

// Send delivers one invoice reminder. errSkip / not-found are terminal
// no-ops (the invoice was paid or deleted between enqueue and send) — treat
// them as success so the worker marks the row sent rather than burning
// retries on a send that can never apply. Real SMTP errors are returned so
// the worker retries up to MaxAttempts.
func (s *InvoiceReminderSender) Send(ctx context.Context, c bcast.Claimed) error {
	if c.InvoiceID == nil {
		return fmt.Errorf("invoice reminder delivery %s has no invoice_id", c.DeliveryID)
	}
	var p invoiceReminderPayload
	if len(c.Payload) > 0 {
		if err := json.Unmarshal(c.Payload, &p); err != nil {
			return fmt.Errorf("unmarshal reminder payload: %w", err)
		}
	}
	err := s.h.sendOneReminder(ctx, c.ClubID, p.ActorID, p.RemoteAddr,
		*c.InvoiceID, p.ClubName, p.DefaultBank, p.Locale, p.IncludeVipps)
	if errors.Is(err, errSkip) || errors.Is(err, errInvoiceNotFound) {
		return nil
	}
	return err
}
