package handlers

import (
	"context"

	"github.com/brygge-klubb/brygge/internal/vipps"
)

// dispatch routes an already-persisted event to the appropriate business logic.
// All operations here are best-effort — the webhook has already been ack'd 200.
func (h *VippsWebhookHandler) dispatch(ctx context.Context, clubID string, evt *vipps.WebhookEvent) {
	switch evt.Name {
	case vipps.EventPaymentCaptured:
		h.onCaptured(ctx, clubID, evt)
	case vipps.EventPaymentCancelled, vipps.EventPaymentAborted, vipps.EventPaymentExpired, vipps.EventPaymentTerminated:
		h.onTerminated(ctx, clubID, evt)
	}
}

func (h *VippsWebhookHandler) onCaptured(ctx context.Context, clubID string, evt *vipps.WebhookEvent) {
	_, err := h.db.Exec(ctx,
		`UPDATE orders SET status = 'paid', paid_at = now()
		 WHERE club_id = $1 AND vipps_reference = $2 AND status = 'pending'`,
		clubID, evt.Reference,
	)
	if err != nil {
		h.log.Error().Err(err).Str("reference", evt.Reference).Msg("webhook captured: order update failed")
	}
}

func (h *VippsWebhookHandler) onTerminated(ctx context.Context, clubID string, evt *vipps.WebhookEvent) {
	_, err := h.db.Exec(ctx,
		`UPDATE orders SET status = 'cancelled'
		 WHERE club_id = $1 AND vipps_reference = $2 AND status = 'pending'`,
		clubID, evt.Reference,
	)
	if err != nil {
		h.log.Error().Err(err).Str("reference", evt.Reference).Msg("webhook terminated: order update failed")
	}
}
