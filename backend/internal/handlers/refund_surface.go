package handlers

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/auth"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/stripe"
)

// How card refunds and disputes show in the lists (BRY-498): the payment
// overview and the faktura list. The lists only read; booking state is the
// pass's (accounting), provider state the webhook's (payment_refunds,
// payment_disputes).

// Refund states of a payment or a faktura.
const (
	refundStateNone    = "none"
	refundStatePartial = "partial"
	refundStateFull    = "full"
)

// Credit states a refund gives a row of the faktura list, next to the manual
// credit states in invoice_credit.go.
const (
	// creditRefund: a credit note a card refund issued. It repays the
	// member through the card, so it is never credit the club still owes.
	creditRefund = "refund"
	// creditRefunded: a faktura whose card refunds credited all of it.
	creditRefunded = "refunded"
)

// refundStateOf says how much of total was refunded: nothing, some of it or
// all of it. Both are in øre.
func refundStateOf(refundedOre, totalOre int64) string {
	switch {
	case refundedOre <= 0 || totalOre <= 0:
		return refundStateNone
	case refundedOre >= totalOre:
		return refundStateFull
	default:
		return refundStatePartial
	}
}

// disputesNeedingResponsePath is the Stripe Dashboard list of disputes that
// wait for evidence, soonest deadline first.
const disputesNeedingResponsePath = "/disputes?statuses[0]=needs_response&statuses[1]=warning_needs_response" +
	"&statuses[2]=appeal_needs_response&sort=evidence_due_by&order=asc"

// stripePaymentDashboardURL links a Stripe payment to its Dashboard page;
// empty for an id that is not a PaymentIntent's (or none).
func stripePaymentDashboardURL(cfg *config.Config, paymentRef string) string {
	if !stripe.ValidPaymentIntentID(paymentRef) {
		return ""
	}
	return stripeDashboardURL(cfg, "/payments/"+paymentRef)
}

// stripeDisputeDashboardURL links a dispute to its Dashboard page; empty
// when there is none.
func stripeDisputeDashboardURL(cfg *config.Config, disputeID string) string {
	if disputeID == "" {
		return ""
	}
	return stripeDashboardURL(cfg, disputeDashboardPath(disputeID))
}

// openDisputeStatusSQL is the condition, on payment_disputes columns, for a
// dispute that still waits for the club to answer.
const openDisputeStatusSQL = `status IN ('needs_response', 'warning_needs_response', 'appeal_needs_response')`

// refundCreditNote is a credit note a card refund issued for a faktura.
type refundCreditNote struct {
	ID     string  `json:"id"`
	Number int     `json:"number"`
	Amount float64 `json:"amount"`
	// HasPDF: the note's PDF can be opened.
	HasPDF bool `json:"has_pdf"`
}

// fakturaRefunds is what card refunds did to one faktura, from
// refund_documents.
type fakturaRefunds struct {
	creditedOre, replacedOre int64
	notes                    []refundCreditNote
	// paymentRef is Stripe's id of the payment refunded, when known.
	paymentRef string
}

// netOre is what stays refunded: the credit notes, less the debit fakturas
// failed refunds issued in their place.
func (f *fakturaRefunds) netOre() int64 {
	if f == nil || f.creditedOre <= f.replacedOre {
		return 0
	}
	return f.creditedOre - f.replacedOre
}

// loadFakturaRefunds reads the live credit notes and replacement debit
// fakturas card refunds issued, by faktura. invoiceIDs limits it to those
// fakturas; nil reads the club's, of which there are few.
func loadFakturaRefunds(ctx context.Context, db *pgxpool.Pool, clubID string, invoiceIDs []string) (map[string]*fakturaRefunds, error) {
	rows, err := db.Query(ctx,
		`SELECT rd.invoice_id::text, rd.kind, rd.amount_ore, doc.id::text, doc.invoice_number,
		        COALESCE(pr.provider_payment_ref, ''),
		        (doc.pdf_data IS NOT NULL OR COALESCE(doc.s3_key, '') <> '')
		   FROM refund_documents rd
		   JOIN invoices doc ON doc.id = rd.credit_invoice_id AND doc.status <> 'voided'
		   LEFT JOIN payment_refunds pr ON pr.id = rd.payment_refund_id
		  WHERE rd.club_id = $1 AND rd.invoice_id IS NOT NULL AND rd.kind IN ('credit', 'refail_invoice')
		    AND ($2::uuid[] IS NULL OR rd.invoice_id = ANY($2::uuid[]))
		  ORDER BY doc.invoice_number`, clubID, invoiceIDs)
	if err != nil {
		return nil, fmt.Errorf("reading refund documents: %w", err)
	}
	defer rows.Close()
	out := map[string]*fakturaRefunds{}
	for rows.Next() {
		var invoiceID, kind, docID, ref string
		var amountOre int64
		var number int
		var hasPDF bool
		if err := rows.Scan(&invoiceID, &kind, &amountOre, &docID, &number, &ref, &hasPDF); err != nil {
			return nil, fmt.Errorf("reading refund document: %w", err)
		}
		f := out[invoiceID]
		if f == nil {
			f = &fakturaRefunds{}
			out[invoiceID] = f
		}
		if kind == "credit" {
			f.creditedOre += amountOre
			f.notes = append(f.notes, refundCreditNote{ID: docID, Number: number, Amount: float64(amountOre) / 100, HasPDF: hasPDF})
		} else {
			f.replacedOre += amountOre
		}
		if f.paymentRef == "" {
			f.paymentRef = ref
		}
	}
	return out, rows.Err()
}

// fakturaDispute is the latest dispute against the payment of a faktura.
type fakturaDispute struct {
	status, disputeID string
	dueBy             *time.Time
}

// loadFakturaDisputes reads the latest dispute of each of the fakturas an
// order paid, by faktura. A dispute belongs to an order by its order id or,
// when that is not known, by its attempt (as the payment overview places it).
func loadFakturaDisputes(ctx context.Context, db *pgxpool.Pool, clubID string, invoiceIDs []string) (map[string]fakturaDispute, error) {
	rows, err := db.Query(ctx,
		`SELECT DISTINCT ON (os.invoice_id) os.invoice_id::text, d.status, d.provider_dispute_id, d.evidence_due_by
		   FROM payment_disputes d
		   JOIN order_sources os
		     ON os.order_id = d.order_id
		     OR EXISTS (SELECT 1 FROM payment_attempts pa WHERE pa.id = d.attempt_id AND pa.order_id = os.order_id)
		  WHERE d.club_id = $1 AND os.invoice_id = ANY($2::uuid[])
		  ORDER BY os.invoice_id, d.created_at DESC, d.id`, clubID, invoiceIDs)
	if err != nil {
		return nil, fmt.Errorf("reading disputes: %w", err)
	}
	defer rows.Close()
	out := map[string]fakturaDispute{}
	for rows.Next() {
		var invoiceID string
		var d fakturaDispute
		if err := rows.Scan(&invoiceID, &d.status, &d.disputeID, &d.dueBy); err != nil {
			return nil, fmt.Errorf("reading dispute: %w", err)
		}
		out[invoiceID] = d
	}
	return out, rows.Err()
}

// addRefundState fills the refund, reissue and dispute fields of the list's
// rows, reading only what the page's fakturas have. A lookup that fails
// leaves the rows without them rather than failing the list: the faktura list
// is the treasurer's daily view. A dispute names a member who charged back,
// so only a treasurer or admin sees it.
func (h *InvoiceHandler) addRefundState(ctx context.Context, claims *auth.Claims, rows []invoiceListRow) {
	ids := make([]string, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}
	if len(ids) == 0 {
		return
	}
	refunds, err := loadFakturaRefunds(ctx, h.db, claims.ClubID, ids)
	if err != nil {
		h.log.Warn().Err(err).Msg("list invoices: refund lookup failed")
		return
	}
	staff := mayRefund(claims.Roles)
	var disputes map[string]fakturaDispute
	if staff {
		if disputes, err = loadFakturaDisputes(ctx, h.db, claims.ClubID, ids); err != nil {
			h.log.Warn().Err(err).Msg("list invoices: dispute lookup failed")
		}
	}
	for i := range rows {
		r := &rows[i]
		r.RefundState, r.RefundCreditNotes = refundStateNone, []refundCreditNote{}
		if r.TotalAmount > 0 {
			f := refunds[r.ID]
			net := f.netOre()
			r.RefundState = refundStateOf(net, int64(math.Round(r.TotalAmount*100)))
			r.RefundedAmount = float64(net) / 100
			if f != nil && net > 0 {
				r.RefundCreditNotes = f.notes
			}
			if r.RefundState == refundStateFull && r.Status == "open" {
				state := creditRefunded
				r.CreditState = &state
				r.CanReissue = staff && r.ReissuedInvoiceID == nil &&
					accounting.ReissueEligible(int64(math.Round(r.TotalAmount*100)), f.creditedOre, f.replacedOre)
			}
			if staff && f != nil {
				r.PaymentURL = stripePaymentDashboardURL(h.config, f.paymentRef)
			}
		}
		if d, ok := disputes[r.ID]; ok {
			status := d.status
			r.DisputeStatus, r.DisputeDueBy = &status, d.dueBy
			r.DisputeURL = stripeDisputeDashboardURL(h.config, d.disputeID)
		}
	}
}

// addMemberRefundState tells a member what card refunds did to their
// fakturas. A failed lookup leaves the fakturas as they are: the member still
// sees them credited or paid.
func (h *MembersHandler) addMemberRefundState(ctx context.Context, clubID string, rows []memberInvoice) {
	var ids []string
	for i := range rows {
		rows[i].RefundState, rows[i].RefundCreditNotes = refundStateNone, []refundCreditNote{}
		if rows[i].TotalAmount > 0 {
			ids = append(ids, rows[i].ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	refunds, err := loadFakturaRefunds(ctx, h.db, clubID, ids)
	if err != nil {
		h.log.Warn().Err(err).Msg("list my invoices: refund lookup failed")
		return
	}
	for i := range rows {
		f := refunds[rows[i].ID]
		net := f.netOre()
		if net == 0 {
			continue
		}
		rows[i].RefundState = refundStateOf(net, int64(math.Round(rows[i].TotalAmount*100)))
		rows[i].RefundedAmount = float64(net) / 100
		rows[i].RefundCreditNotes = f.notes
	}
}
