package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/finance"
)

// The documents a card refund issues (ACC-1, ACC-2, ACC-9). A refund that
// reduces a sale issues a credit note against the faktura (or, for a QR or
// catalogue order with no faktura, against the order) for the refunded
// amount, booked at once (DR revenue / CR 1500) and marked sent. A refund
// whose money comes back (refund_failure) issues a new debit faktura for it
// instead of voiding the credit note. Both are created inside the booking
// pass's transaction through CreateInvoiceTx, booked through it, and queued
// for delivery on it (deliveries, kind invoice_refund_document): the PDF is
// rendered and the email sent by the delivery worker after the commit,
// never while the pass holds its locks or the club's Stripe slot.
//
// A refund credit note is linked to its faktura through refund_documents,
// never credits_invoice_id: that link (and idx_invoices_credits_once) keeps
// meaning a manual credit of the whole faktura (BRY-342).

// refundTarget is one faktura (or one order with no faktura) a refund is
// divided over, with what the documents it issues copy.
type refundTarget struct {
	invoiceID *string // nil for an order's catalogue lines
	orderID   *string
	number    int // the faktura number; 0 for an order target
	// shareOre is what the charge paid for this target: the faktura total,
	// or the order's catalogue lines.
	shareOre int64
	// creditedOre is what earlier refunds of it credited already.
	creditedOre int64
	status      string
	// manuallyCredited: a live credit note is linked to it by hand.
	manuallyCredited bool
	recipient        documentRecipient
}

// label names the target in descriptions.
func (t *refundTarget) label() string {
	if t.invoiceID != nil {
		return fmt.Sprintf("faktura %d", t.number)
	}
	return "ordre " + shortOrderID(derefOr(t.orderID, ""))
}

// documentRecipient is who a credit note or debit faktura bills: the same
// payer as what it corrects. displayName names them in the entry
// description; deliverTo is where the document is emailed ("" for none).
type documentRecipient struct {
	userID                 *string
	recipientKind          string
	orgName, orgNumber     *string
	orgAddress, contact    *string
	theirRef, email        *string
	recipientID            *string
	memberName             string
	memberEmail            string
	displayName, deliverTo string
}

// documentLine is a line a document copies, in øre.
type documentLine struct {
	description, subDescription string
	amountOre                   int64
	// quantity and unitPriceOre are the line as it was issued; a line that
	// had an exemption folded into it has netted set and is one unit of its
	// net amount.
	quantity                int
	unitPriceOre            int64
	netted                  bool
	accountID, priceItemID  *string
	category, exemptionRole *string
	boatID                  *string
}

func derefOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

// loadInvoiceRecipientTx reads a faktura's payer as a document copies it.
func loadInvoiceRecipientTx(ctx context.Context, tx pgx.Tx, clubID, invoiceID string) (documentRecipient, error) {
	var r documentRecipient
	err := tx.QueryRow(ctx,
		`SELECT i.user_id::text, COALESCE(i.recipient_kind, 'private'), i.recipient_org_name, i.recipient_org_number,
		        i.recipient_org_address, i.recipient_contact_person, i.recipient_their_ref, i.recipient_email,
		        i.recipient_id::text, COALESCE(i.member_name, ''), COALESCE(i.member_email, ''),
		        COALESCE(NULLIF(i.recipient_org_name, ''), NULLIF(i.member_name, ''), u.full_name, ''),
		        COALESCE(NULLIF(i.recipient_email, ''), NULLIF(i.member_email, ''), u.email, '')
		   FROM invoices i
		   LEFT JOIN users u ON u.id = i.user_id
		  WHERE i.id = $1 AND i.club_id = $2`,
		invoiceID, clubID,
	).Scan(&r.userID, &r.recipientKind, &r.orgName, &r.orgNumber, &r.orgAddress, &r.contact, &r.theirRef,
		&r.email, &r.recipientID, &r.memberName, &r.memberEmail, &r.displayName, &r.deliverTo)
	if err != nil {
		return r, fmt.Errorf("loading faktura payer: %w", err)
	}
	return r, nil
}

// loadOrderRecipientTx reads an order's payer: its member, or the guest's
// name and email (a guest without an email gets the document as a PDF only).
func loadOrderRecipientTx(ctx context.Context, tx pgx.Tx, clubID, orderID string) (documentRecipient, error) {
	r := documentRecipient{recipientKind: "private"}
	var guestName, guestEmail, fullName, userEmail string
	err := tx.QueryRow(ctx,
		`SELECT o.user_id::text, o.guest_name, o.guest_email, COALESCE(u.full_name, ''), COALESCE(u.email, '')
		   FROM orders o
		   LEFT JOIN users u ON u.id = o.user_id
		  WHERE o.id = $1 AND o.club_id = $2`,
		orderID, clubID,
	).Scan(&r.userID, &guestName, &guestEmail, &fullName, &userEmail)
	if err != nil {
		return r, fmt.Errorf("loading order payer: %w", err)
	}
	if r.userID != nil {
		r.memberName, r.memberEmail = fullName, userEmail
		r.displayName, r.deliverTo = fullName, userEmail
		return r, nil
	}
	r.memberName, r.displayName, r.deliverTo = guestName, guestName, guestEmail
	if guestEmail != "" {
		r.email = &guestEmail
	}
	return r, nil
}

// loadTargetLinesTx reads the lines a target's credit note copies: the
// faktura's lines (category as stored at issue), or the order's catalogue
// lines (category from the price item, as the order's charge booked them).
func loadTargetLinesTx(ctx context.Context, tx pgx.Tx, t *refundTarget) ([]documentLine, error) {
	if t.invoiceID != nil {
		return loadInvoiceLinesTx(ctx, tx, *t.invoiceID)
	}
	rows, err := tx.Query(ctx,
		`SELECT ol.name, round(ol.total_price * 100)::bigint, ol.price_item_id::text, NULLIF(pi.category, '')
		   FROM order_lines ol
		   LEFT JOIN price_items pi ON pi.id = ol.price_item_id
		  WHERE ol.order_id = $1
		  ORDER BY ol.created_at, ol.id`,
		derefOr(t.orderID, ""))
	if err != nil {
		return nil, fmt.Errorf("loading order lines: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (documentLine, error) {
		var l documentLine
		err := r.Scan(&l.description, &l.amountOre, &l.priceItemID, &l.category)
		if l.priceItemID != nil && l.category == nil {
			// The line check requires a category with a price item; the
			// revenue account falls back either way.
			l.priceItemID = nil
		}
		return l, err
	})
}

// loadInvoiceLinesTx reads a faktura's lines with the category each books
// by (finance.LineCategorySQL).
func loadInvoiceLinesTx(ctx context.Context, tx pgx.Tx, invoiceID string) ([]documentLine, error) {
	rows, err := tx.Query(ctx,
		`SELECT il.description, COALESCE(il.sub_description, ''), round(il.line_total * 100)::bigint,
		        il.quantity, round(il.unit_price * 100)::bigint,
		        il.account_id::text, il.price_item_id::text, `+finance.LineCategorySQL("il", "pi")+`,
		        il.exemption_role, il.boat_id::text
		   FROM invoice_lines il
		   LEFT JOIN price_items pi ON pi.id = il.price_item_id
		  WHERE il.invoice_id = $1
		  ORDER BY il.id`,
		invoiceID)
	if err != nil {
		return nil, fmt.Errorf("loading faktura lines: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (documentLine, error) {
		var l documentLine
		err := r.Scan(&l.description, &l.subDescription, &l.amountOre, &l.quantity, &l.unitPriceOre,
			&l.accountID, &l.priceItemID, &l.category, &l.exemptionRole, &l.boatID)
		return l, err
	})
}

// netExemptions folds each role-exemption line into the item line it
// offsets (same price item and boat), so a credit note credits what the
// member actually paid for; a fully exempt pair drops out, and so does any
// other line that nets to nothing.
func netExemptions(lines []documentLine) []documentLine {
	items, orphans := foldExemptions(lines)
	return dropZeroLines(append(items, orphans...))
}

// foldExemptions folds each exemption line into the item line it offsets
// (same price item and boat). The folded items come back with netted set;
// an exemption with no such item is an orphan, returned apart.
func foldExemptions(lines []documentLine) (items, orphans []documentLine) {
	items = make([]documentLine, 0, len(lines))
	var exemptions []documentLine
	for _, l := range lines {
		if l.exemptionRole != nil {
			exemptions = append(exemptions, l)
			continue
		}
		items = append(items, l)
	}
	for _, e := range exemptions {
		folded := false
		for i := range items {
			if sameRef(items[i].priceItemID, e.priceItemID) && sameRef(items[i].boatID, e.boatID) && items[i].amountOre > 0 {
				items[i].amountOre += e.amountOre
				items[i].netted = true
				folded = true
				break
			}
		}
		if !folded {
			orphans = append(orphans, e)
		}
	}
	return items, orphans
}

func dropZeroLines(lines []documentLine) []documentLine {
	kept := lines[:0]
	for _, l := range lines {
		if l.amountOre != 0 {
			kept = append(kept, l)
		}
	}
	return kept
}

func sameRef(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// errRefundNotScalable: the target's lines net to nothing to divide the
// refund over.
var errRefundNotScalable = errors.New("the lines net to zero")

// scaleLines divides amountOre over lines in proportion to their amounts,
// truncating each share and putting the remainder on the largest line, so
// the result sums to amountOre exactly.
func scaleLines(lines []documentLine, amountOre int64) ([]int64, error) {
	var total int64
	largest := -1
	for i, l := range lines {
		total += l.amountOre
		if largest < 0 || abs64(l.amountOre) > abs64(lines[largest].amountOre) {
			largest = i
		}
	}
	if total <= 0 || largest < 0 {
		return nil, errRefundNotScalable
	}
	out := make([]int64, len(lines))
	var sum int64
	for i, l := range lines {
		out[i] = amountOre * l.amountOre / total
		sum += out[i]
	}
	out[largest] += amountOre - sum
	return out, nil
}

// allocateRefund divides refundOre over targets: a partial refund in
// proportion to each target's share of the charge (truncated, the
// remainder on the last), a refund that completes the charge's refunds as
// what each target has left uncredited, so a faktura refunded in full
// through partials is credited to the øre. ok is false when the targets
// cannot take it exactly (one is credited past its share).
func allocateRefund(targets []*refundTarget, refundOre int64, full bool) (alloc []int64, ok bool) {
	alloc = make([]int64, len(targets))
	var totalShare, sum int64
	for _, t := range targets {
		totalShare += t.shareOre
	}
	if totalShare <= 0 || len(targets) == 0 {
		return nil, false
	}
	for i, t := range targets {
		if full {
			alloc[i] = t.shareOre - t.creditedOre
		} else {
			alloc[i] = refundOre * t.shareOre / totalShare
		}
		sum += alloc[i]
	}
	if !full {
		alloc[len(alloc)-1] += refundOre - sum
		sum = refundOre
	}
	if sum != refundOre {
		return nil, false
	}
	for i, t := range targets {
		if alloc[i] < 0 || alloc[i] > t.shareOre-t.creditedOre {
			return nil, false
		}
	}
	return alloc, true
}

// issuedDocument is a credit note or debit faktura a refund booking issued.
type issuedDocument struct {
	ID            string
	Number        int
	InvoiceNumber int // the faktura it corrects; 0 for an order
	AmountOre     int64
}

// issueRefundCreditNoteTx issues, books and queues for delivery a credit
// note for amountOre of the target, dated the refund's booking date, so it
// lands in the same period as the refund (ACC-2). Its lines are the
// target's net of exemptions, negated and scaled to the amount.
func issueRefundCreditNoteTx(ctx context.Context, tx pgx.Tx, clubID, actorID string, t *refundTarget,
	amountOre int64, date time.Time, periodID string) (*issuedDocument, error) {
	source, err := loadTargetLinesTx(ctx, tx, t)
	if err != nil {
		return nil, err
	}
	lines := netExemptions(source)
	amounts, err := scaleLines(lines, amountOre)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", t.label(), err)
	}
	header := fmt.Sprintf("Kreditnota for %s – refusjon kortbetaling %s", t.label(), date.Format("02.01.2006"))
	draftLines := make([]InvoiceDraftLine, 0, len(lines))
	for i, l := range lines {
		if amounts[i] == 0 {
			continue
		}
		draftLines = append(draftLines, documentDraftLine(l, -amounts[i], header))
	}
	return issueDocumentTx(ctx, tx, clubID, actorID, t.recipient, draftLines, date, periodID)
}

// documentDraftLine is l as a one-unit line of amountOre, with note as its
// sub-description.
func documentDraftLine(l documentLine, amountOre int64, note string) InvoiceDraftLine {
	return InvoiceDraftLine{
		Description:    l.description,
		SubDescription: note,
		Quantity:       1,
		UnitPrice:      oreToKr(amountOre),
		AccountID:      l.accountID,
		PriceItemID:    l.priceItemID,
		Category:       l.category,
		BoatID:         l.boatID,
	}
}

// issueDocumentTx creates a document dated date, marks it sent, books it
// in periodID and queues its delivery, all on tx.
func issueDocumentTx(ctx context.Context, tx pgx.Tx, clubID, actorID string, r documentRecipient,
	lines []InvoiceDraftLine, date time.Time, periodID string) (*issuedDocument, error) {
	if len(lines) == 0 {
		return nil, errRefundNotScalable
	}
	created, err := CreateInvoiceTx(ctx, tx, clubID, InvoiceDraft{
		UserID:                 r.userID,
		IssueDate:              date,
		DueDate:                date,
		FiscalPeriodID:         &periodID,
		RecipientKind:          r.recipientKind,
		RecipientOrgName:       r.orgName,
		RecipientOrgNumber:     r.orgNumber,
		RecipientOrgAddress:    r.orgAddress,
		RecipientContactPerson: r.contact,
		RecipientTheirRef:      r.theirRef,
		RecipientEmail:         r.email,
		RecipientID:            r.recipientID,
		MemberName:             r.memberName,
		MemberEmail:            r.memberEmail,
		Sent:                   true,
		Lines:                  lines,
	})
	if err != nil {
		return nil, err
	}
	var name *string
	if r.displayName != "" {
		name = &r.displayName
	}
	if _, err := bookInvoiceEntryTx(ctx, tx, clubID, periodID, created.ID, created.Number, created.Total,
		date.Format("2006-01-02"), name, actorID); err != nil {
		return nil, fmt.Errorf("booking document %d: %w", created.Number, err)
	}
	// The worker renders the PDF and, when there is an address, emails it.
	if _, err := tx.Exec(ctx,
		`INSERT INTO deliveries (kind, invoice_id, club_id, user_id, email, status)
		 VALUES ($1, $2, $3, $4, $5, 'pending')`,
		bcast.KindInvoiceRefundDocument, created.ID, clubID, r.userID, r.deliverTo,
	); err != nil {
		return nil, fmt.Errorf("queueing document %d: %w", created.Number, err)
	}
	return &issuedDocument{ID: created.ID, Number: created.Number, AmountOre: abs64(toCents(created.Total))}, nil
}

// issueRefailInvoiceTx issues the debit faktura for a refund credit note
// whose money came back: the note's lines with the sign flipped, to the same
// payer (ACC-9: the credit note stands). The caller settles it with the
// returned money.
func issueRefailInvoiceTx(ctx context.Context, tx pgx.Tx, clubID, actorID, creditNoteID string,
	date time.Time, periodID string) (*issuedDocument, error) {
	recipient, err := loadInvoiceRecipientTx(ctx, tx, clubID, creditNoteID)
	if err != nil {
		return nil, err
	}
	source, err := loadInvoiceLinesTx(ctx, tx, creditNoteID)
	if err != nil {
		return nil, err
	}
	note := "Ny faktura etter mislykket refusjon " + date.Format("02.01.2006")
	lines := make([]InvoiceDraftLine, 0, len(source))
	for _, l := range source {
		if l.amountOre != 0 {
			lines = append(lines, documentDraftLine(l, -l.amountOre, note))
		}
	}
	return issueDocumentTx(ctx, tx, clubID, actorID, recipient, lines, date, periodID)
}
