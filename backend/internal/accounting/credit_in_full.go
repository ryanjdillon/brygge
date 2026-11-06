package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrNothingToCredit: the faktura's lines net to nothing, so there is no
// credit note to issue.
var ErrNothingToCredit = errors.New("the faktura's lines net to zero")

// ErrNotIssued: the faktura was never sent, so there is no issued document to
// credit. A sent credit note against it would be booked on its own and drive
// the receivable negative.
var ErrNotIssued = errors.New("the faktura was never issued")

// CreditedInFull is the credit note CreditInvoiceInFullTx issued.
type CreditedInFull struct {
	CreatedInvoice
	// DeliverTo and DisplayName are where to email the credit note and whom
	// it is for, as the faktura it credits was billed; DeliverTo is empty
	// for a payer with no address.
	DeliverTo   string
	DisplayName string
}

// CreditInvoiceInFullTx issues a credit note for the whole of invoiceID on tx
// and links it as a full credit (credits_invoice_id), as a treasurer's credit
// note for the whole faktura is (ACC-1): the faktura's lines net of
// exemptions, negated, to the same payer, with note as each line's
// sub-description, dated issueDate. It is numbered under the club's numbering
// lock like every other faktura. The faktura itself is never changed: the link
// is on the credit note, and idx_invoices_credits_once refuses a second full
// credit.
//
// The credit note is issued (sent) exactly as the faktura is: an unsent
// faktura is refused with ErrNotIssued, so a sent credit note always has a
// sent faktura beside it. Booking stays the caller's (Service.BookInvoice
// after the commit); whatever it misses, SyncInvoices books, and it books
// every sent faktura, so the pair always ends up both in the GL. The caller
// holds the faktura's row lock and has checked it is still owed.
func CreditInvoiceInFullTx(ctx context.Context, tx pgx.Tx, clubID, invoiceID, note string, issueDate time.Time) (*CreditedInFull, error) {
	var sent bool
	if err := tx.QueryRow(ctx,
		`SELECT sent_at IS NOT NULL FROM invoices WHERE id = $1 AND club_id = $2`, invoiceID, clubID,
	).Scan(&sent); err != nil {
		return nil, fmt.Errorf("loading faktura: %w", err)
	}
	if !sent {
		return nil, ErrNotIssued
	}
	recipient, err := loadInvoiceRecipientTx(ctx, tx, clubID, invoiceID)
	if err != nil {
		return nil, err
	}
	source, err := loadInvoiceLinesTx(ctx, tx, invoiceID)
	if err != nil {
		return nil, err
	}
	lines := make([]InvoiceDraftLine, 0, len(source))
	for _, l := range netExemptions(source) {
		lines = append(lines, documentDraftLine(l, -l.amountOre, note))
	}
	if len(lines) == 0 {
		return nil, ErrNothingToCredit
	}
	created, err := CreateInvoiceTx(ctx, tx, clubID, InvoiceDraft{
		UserID:                 recipient.userID,
		IssueDate:              issueDate,
		DueDate:                issueDate,
		RecipientKind:          recipient.recipientKind,
		RecipientOrgName:       recipient.orgName,
		RecipientOrgNumber:     recipient.orgNumber,
		RecipientOrgAddress:    recipient.orgAddress,
		RecipientContactPerson: recipient.contact,
		RecipientTheirRef:      recipient.theirRef,
		RecipientEmail:         recipient.email,
		RecipientID:            recipient.recipientID,
		MemberName:             recipient.memberName,
		MemberEmail:            recipient.memberEmail,
		Sent:                   true,
		Lines:                  lines,
	})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE invoices SET credits_invoice_id = $3 WHERE id = $1 AND club_id = $2`,
		created.ID, clubID, invoiceID,
	); err != nil {
		return nil, fmt.Errorf("linking credit note %d: %w", created.Number, err)
	}
	return &CreditedInFull{CreatedInvoice: *created, DeliverTo: recipient.deliverTo, DisplayName: recipient.displayName}, nil
}
