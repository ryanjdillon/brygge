package accounting

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// "Ny faktura" after a card refund (BRY-498, ACC-8). A refund that credited a
// whole faktura leaves the member owing nothing; when the club still wants the
// money, the treasurer issues a fresh faktura with the same lines. The new
// faktura is an ordinary draft here: the caller sends and books it through
// the same path as a faktura made by hand, so it looks like any other.
//
// The credit notes the refund issued stand (ACC-9), so this never touches
// them. invoices.reissued_as_invoice_id on the credited faktura makes the
// action once-only: a second request finds it set under the faktura's row
// lock. A new faktura that was voided no longer counts, so another can be
// issued (the link is overwritten); deleting a draft clears it (ON DELETE SET
// NULL).

// Codes of a ReissueRefusal.
const (
	// ReissueNotRefunded: the faktura is not credited in full by card
	// refunds, or its refund was undone by a failed refund (the debit
	// faktura that replaced the credit already collected the money again).
	ReissueNotRefunded = "not_refunded"
	// ReissueAlreadyReissued: a new faktura was issued for it before.
	ReissueAlreadyReissued = "already_reissued"
)

// ErrReissueNotFound: no such faktura in the club.
var ErrReissueNotFound = errors.New("faktura not found")

// ReissueEligible is the one rule for whether a faktura may be issued again,
// used by the endpoint and by the list that offers the button, so they never
// disagree. creditedOre is what its live refund credit notes add up to and
// replacedOre what the debit fakturas failed refunds issued in their place
// add up to, both in øre. It must be credited in full and never re-billed: a
// faktura whose failed refund was already collected again has no credit left
// to collect, even if a later refund credited it once more.
func ReissueEligible(totalOre, creditedOre, replacedOre int64) bool {
	return totalOre > 0 && replacedOre == 0 && creditedOre == totalOre
}

// ReissueRefusal is a reason a faktura cannot be issued again.
type ReissueRefusal struct {
	Code    string
	Message string
}

func (e *ReissueRefusal) Error() string { return e.Message }

// ReissuedInvoice is the draft faktura ReissueAfterRefund stored.
type ReissuedInvoice struct {
	ID             string
	Number         int
	KID            string
	Total          float64
	IssueDate      time.Time
	DueDate        time.Time
	OriginalNumber int
	// DeliverTo and DisplayName are where to email it and whom it is for;
	// DeliverTo is empty for a payer with no address.
	DeliverTo   string
	DisplayName string
}

// ReissueAfterRefund stores a new, unsent faktura copying the lines of the
// credited faktura invoiceID (net of role exemptions, as the credit note
// that credited it was), to the same payer, and records it on the credited
// faktura. It numbers under the club's numbering lock like every other
// faktura. It refuses unless the credit notes card refunds issued for the
// faktura add up to its whole total.
func (s *Service) ReissueAfterRefund(ctx context.Context, clubID, invoiceID string, issueDate, dueDate time.Time) (*ReissuedInvoice, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning reissue: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rolled back unless committed

	var (
		number   int
		totalOre int64
		status   string
		// liveReissue: a new faktura was issued and still stands.
		liveReissue bool
	)
	// NO KEY UPDATE like lockInvoicesTx, which the refund booking takes on
	// the same fakturas: the two queue, and writes that only reference the
	// faktura by foreign key do not. The state is read in a second
	// statement, so a request that waited for the lock sees the link the
	// one before it wrote.
	var locked int
	err = tx.QueryRow(ctx,
		`SELECT 1 FROM invoices WHERE id = $1 AND club_id = $2 FOR NO KEY UPDATE`, invoiceID, clubID,
	).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrReissueNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("locking faktura: %w", err)
	}
	if err := tx.QueryRow(ctx,
		`SELECT i.invoice_number, round(i.total_amount * 100)::bigint, i.status,
		        EXISTS (SELECT 1 FROM invoices n WHERE n.id = i.reissued_as_invoice_id AND n.status <> 'voided')
		   FROM invoices i WHERE i.id = $1`, invoiceID,
	).Scan(&number, &totalOre, &status, &liveReissue); err != nil {
		return nil, fmt.Errorf("loading faktura: %w", err)
	}
	if liveReissue {
		return nil, &ReissueRefusal{Code: ReissueAlreadyReissued, Message: "a new faktura was already issued for this one"}
	}
	notRefunded := &ReissueRefusal{Code: ReissueNotRefunded, Message: "the faktura is not credited in full by card refunds"}
	if status == "voided" {
		return nil, notRefunded
	}

	// What card refunds credited: the live credit notes, less the debit
	// fakturas failed refunds issued in their place (that money was
	// collected again and settled).
	var creditedOre, replacedOre int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(rd.amount_ore) FILTER (WHERE rd.kind = 'credit'), 0)::bigint,
		        COALESCE(SUM(rd.amount_ore) FILTER (WHERE rd.kind = 'refail_invoice'), 0)::bigint
		   FROM refund_documents rd
		   JOIN invoices doc ON doc.id = rd.credit_invoice_id AND doc.status <> 'voided'
		  WHERE rd.invoice_id = $1`, invoiceID,
	).Scan(&creditedOre, &replacedOre); err != nil {
		return nil, fmt.Errorf("reading the faktura's refund credit notes: %w", err)
	}
	if !ReissueEligible(totalOre, creditedOre, replacedOre) {
		return nil, notRefunded
	}

	recipient, err := loadInvoiceRecipientTx(ctx, tx, clubID, invoiceID)
	if err != nil {
		return nil, err
	}
	// The credit note mirrors the document it credited, so it keeps the
	// payer as billed; a new faktura goes to where the member is now.
	if err := useCurrentPayerTx(ctx, tx, clubID, &recipient); err != nil {
		return nil, err
	}
	source, err := loadInvoiceLinesTx(ctx, tx, invoiceID)
	if err != nil {
		return nil, err
	}
	// An exemption folds into the item it reduced. One with no item to fold
	// into (the item is not on the faktura) is not copied: a discount on
	// something not billed is not what the member paid for, and nothing says
	// what it would apply to. The new faktura is then the items at what they
	// cost the member, which can be more than the credited total.
	items, _ := foldExemptions(source)
	lines := make([]InvoiceDraftLine, 0, len(items))
	for _, l := range dropZeroLines(items) {
		lines = append(lines, reissueLine(l))
	}
	if len(lines) == 0 {
		return nil, notRefunded
	}

	created, err := CreateInvoiceTx(ctx, tx, clubID, InvoiceDraft{
		UserID:                 recipient.userID,
		IssueDate:              issueDate,
		DueDate:                dueDate,
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
		Lines:                  lines,
	})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE invoices SET reissued_as_invoice_id = $3 WHERE id = $1 AND club_id = $2`,
		invoiceID, clubID, created.ID); err != nil {
		return nil, fmt.Errorf("recording the new faktura: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing reissue: %w", err)
	}
	return &ReissuedInvoice{
		ID: created.ID, Number: created.Number, KID: created.KID, Total: created.Total,
		IssueDate: issueDate, DueDate: dueDate, OriginalNumber: number,
		DeliverTo: recipient.deliverTo, DisplayName: recipient.displayName,
	}, nil
}

// useCurrentPayerTx points a payer read from a faktura at the member as they
// are now: their current name and, unless the faktura names its own
// recipient address (an organisation's), their current email. A faktura with
// no member is left as billed.
func useCurrentPayerTx(ctx context.Context, tx pgx.Tx, clubID string, r *documentRecipient) error {
	if r.userID == nil {
		return nil
	}
	var name, mail string
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(full_name, ''), COALESCE(email, '') FROM users WHERE id = $1 AND club_id = $2`,
		*r.userID, clubID,
	).Scan(&name, &mail); err != nil {
		return fmt.Errorf("loading the member: %w", err)
	}
	if name != "" {
		r.memberName = name
		if r.orgName == nil || *r.orgName == "" {
			r.displayName = name
		}
	}
	if mail != "" {
		r.memberEmail = mail
		if r.email == nil || *r.email == "" {
			r.deliverTo = mail
		}
	}
	return nil
}

// reissueLine is a source line as the new faktura's line: as it was issued
// (quantity and unit price) unless an exemption was folded into it, when it
// is one unit of the net amount.
func reissueLine(l documentLine) InvoiceDraftLine {
	qty, unit := 1, oreToKr(l.amountOre)
	if !l.netted && l.quantity > 0 && int64(l.quantity)*l.unitPriceOre == l.amountOre {
		qty, unit = l.quantity, oreToKr(l.unitPriceOre)
	}
	return InvoiceDraftLine{
		Description:    l.description,
		SubDescription: l.subDescription,
		Quantity:       qty,
		UnitPrice:      unit,
		AccountID:      l.accountID,
		PriceItemID:    l.priceItemID,
		Category:       l.category,
		BoatID:         l.boatID,
	}
}
