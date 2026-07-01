package broadcast

import (
	"context"
	"fmt"
)

// BroadcastSender is the Sender for broadcast-kind deliveries. It loads the
// parent broadcast's content (moved off the old ClaimPending JOIN, which no
// longer applies now that the queue is shared with parent-less invoice
// rows) and delivers one individual message per recipient through the
// underlying MessageSender (the inbox handler's principal-send path).
type BroadcastSender struct {
	store  *Store
	sender MessageSender
}

// NewBroadcastSender wires the broadcast Sender to the queue store (for the
// content load) and the message backend.
func NewBroadcastSender(store *Store, sender MessageSender) *BroadcastSender {
	return &BroadcastSender{store: store, sender: sender}
}

// Send renders and delivers one broadcast recipient's message.
func (b *BroadcastSender) Send(ctx context.Context, c Claimed) error {
	if c.BroadcastID == nil {
		return fmt.Errorf("broadcast delivery %s has no broadcast_id", c.DeliveryID)
	}
	content, err := b.store.LoadBroadcastContent(ctx, *c.BroadcastID)
	if err != nil {
		return err
	}
	return b.sender.SendBroadcast(ctx, content.SourceAddress, OutgoingMessage{
		To:              c.Email,
		Subject:         content.Subject,
		BodyText:        content.BodyText,
		BodyHTML:        content.BodyHTML,
		AttachBodyParts: content.Attachments,
		UserID:          c.UserID,
	})
}
