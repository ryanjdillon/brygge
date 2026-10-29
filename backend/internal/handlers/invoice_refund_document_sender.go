package handlers

import (
	"context"
	"fmt"

	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/email"
)

// RefundDocumentSender is the broadcast.Sender for 'invoice_refund_document'
// deliveries: a credit note a card refund issued, or the debit faktura a
// failed refund issued. The booking pass creates and books the document and
// queues this row in the same transaction; here, after the commit and away
// from the pass's locks, the PDF is rendered and stored and, when the row
// has an address, the document is emailed. A failure is retried by the
// worker and, once it gives up, shows as a failed delivery in the faktura's
// delivery log.
type RefundDocumentSender struct {
	h *InvoiceHandler
}

// NewRefundDocumentSender binds the sender to the invoice handler (which
// owns the SMTP client, storage and the PDF inputs).
func NewRefundDocumentSender(h *InvoiceHandler) *RefundDocumentSender {
	return &RefundDocumentSender{h: h}
}

// Send renders the document's PDF unless one is stored already (a retry),
// then emails it to c.Email; with no address the PDF is all there is.
func (s *RefundDocumentSender) Send(ctx context.Context, c bcast.Claimed) error {
	if c.InvoiceID == nil {
		return fmt.Errorf("refund document delivery %s has no invoice_id", c.DeliveryID)
	}
	h := s.h
	club, err := loadClubInvoiceFields(ctx, h.db, c.ClubID)
	if err != nil {
		return fmt.Errorf("loading club: %w", err)
	}
	pdf, number, total, err := h.ensureInvoicePDF(ctx, c.ClubID, *c.InvoiceID, club)
	if err != nil {
		return err
	}
	// Without mail the PDF is all there is, and the row is done: it must not
	// wait to be emailed if SMTP is configured later.
	if c.Email == "" || h.email == nil {
		return nil
	}
	var name string
	if err := h.db.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(i.recipient_org_name, ''), NULLIF(i.member_name, ''), u.full_name, '')
		   FROM invoices i LEFT JOIN users u ON u.id = i.user_id
		  WHERE i.id = $1 AND i.club_id = $2`, *c.InvoiceID, c.ClubID,
	).Scan(&name); err != nil {
		return fmt.Errorf("loading recipient: %w", err)
	}
	subject := email.RefundDocumentSubject(club.Name, number, total)
	body := email.RefundDocumentBody(name, club.Name, number, total)
	filename := fmt.Sprintf("faktura-%d.pdf", number)
	if total < 0 {
		filename = fmt.Sprintf("kreditnota-%d.pdf", number)
	}
	return h.email.SendWithAttachment(email.WithKind(ctx, email.KindInvoice), c.Email, subject, body, filename, pdf)
}
