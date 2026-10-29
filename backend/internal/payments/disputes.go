package payments

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Card disputes a provider reports against a payment (payment_disputes).
// The provider's webhook is the only writer and writes nothing else: the
// loss and its reversal are booked from the provider's balance transactions
// (accounting), never from this table. It exists so that the treasurer can
// be told, a refund can be refused while a dispute stands in the way, and an
// inbox can list what is waiting for evidence.

// Querier is the part of a pgx pool or transaction the dispute store uses,
// so a caller holding a transaction (a refund locking its attempt) reads
// under it.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Dispute is what a provider event says about one dispute. Every field but
// the club, the provider and the dispute id may be unknown: the event's
// shape is not trusted.
type Dispute struct {
	ClubID            string
	Provider          string
	ProviderDisputeID string
	// AttemptID and OrderID name the payment the dispute is about; empty
	// when Brygge cannot tie it to one.
	AttemptID string
	OrderID   string
	// ChargeRef and PaymentRef are the provider's ids of the charge and of
	// the payment it belongs to.
	ChargeRef  string
	PaymentRef string
	// AmountOre is the disputed amount in minor units; nil when unknown.
	AmountOre *int64
	Currency  string
	Reason    string
	// Status is the provider's own word for where the dispute stands.
	Status        string
	EvidenceDueBy *time.Time
	// ChargeRefundable is the provider's own answer to whether the payment
	// can still be refunded (Stripe's is_charge_refundable); nil when the
	// event left it out.
	ChargeRefundable *bool
}

// Dispute statuses (Stripe's). A dispute starts as an inquiry (warning_*)
// that the bank may escalate to a chargeback, under the same id, or close
// without taking money; a chargeback ends won, lost, refunded or prevented.
const (
	DisputeWarningNeedsResponse = "warning_needs_response"
	DisputeWarningUnderReview   = "warning_under_review"
	DisputeWarningClosed        = "warning_closed"
	DisputeNeedsResponse        = "needs_response"
	DisputeUnderReview          = "under_review"
	DisputeWon                  = "won"
	DisputeLost                 = "lost"
	DisputeChargeRefunded       = "charge_refunded"
	DisputePrevented            = "prevented"
)

// disputeRankSQL orders a dispute status by how far the dispute has got:
// 1-3 the inquiry (an inquiry that closed can still be escalated), 4-5 the
// chargeback, 6 the end of a chargeback, which nothing follows. A status
// this code does not know ranks 0.
func disputeRankSQL(status string) string {
	return `(CASE ` + status + `
	    WHEN 'warning_needs_response' THEN 1
	    WHEN 'warning_under_review' THEN 2
	    WHEN 'warning_closed' THEN 3
	    WHEN 'needs_response' THEN 4
	    WHEN 'under_review' THEN 5
	    WHEN 'won' THEN 6 WHEN 'lost' THEN 6 WHEN 'charge_refunded' THEN 6 WHEN 'prevented' THEN 6
	    ELSE 0 END)`
}

// disputePhaseSQL is the phase of a dispute that is waiting for the club
// to answer, for telling the treasurer: 1 an inquiry, 2 a chargeback; 0 for
// one that is closed or unknown.
const disputePhaseSQL = `(CASE status
	    WHEN 'warning_needs_response' THEN 1 WHEN 'warning_under_review' THEN 1
	    WHEN 'needs_response' THEN 2 WHEN 'under_review' THEN 2
	    ELSE 0 END)`

// UpsertDispute records a dispute event and reports whether the treasurer
// is to be told about it now. The upsert is idempotent under redelivery and
// tolerant of events arriving out of order: an event never moves a dispute
// to an earlier status (an inquiry escalating to a chargeback is a move
// forward), a finished dispute is final, and fields the event leaves out keep
// what is stored.
//
// The treasurer is told once for each phase a dispute is open in: when it is
// first stored as an inquiry or a chargeback, and again when an inquiry
// becomes a chargeback (a new deadline). A dispute stored already closed
// (its first event arrived late) is not told. The notice is claimed with a
// guarded update, so concurrent or repeated deliveries tell once.
func UpsertDispute(ctx context.Context, q Querier, d Dispute) (notify bool, err error) {
	if d.ClubID == "" || d.Provider == "" || d.ProviderDisputeID == "" {
		return false, errors.New("a dispute needs a club, a provider and a dispute id")
	}
	var attempt, order *string
	if d.AttemptID != "" {
		attempt = &d.AttemptID
	}
	if d.OrderID != "" {
		order = &d.OrderID
	}
	newRank, oldRank := disputeRankSQL("EXCLUDED.status"), disputeRankSQL("p.status")
	_, err = q.Exec(ctx,
		`INSERT INTO payment_disputes AS p
		     (club_id, attempt_id, order_id, provider, provider_dispute_id, charge_ref, provider_payment_ref,
		      amount, currency, reason, status, evidence_due_by, is_charge_refundable)
		 VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''),
		         $8::bigint / 100.0, COALESCE(NULLIF(upper($9), ''), 'NOK'), $10, $11, $12, $13)
		 ON CONFLICT (provider, provider_dispute_id) DO UPDATE
		    SET attempt_id           = COALESCE(EXCLUDED.attempt_id, p.attempt_id),
		        order_id             = COALESCE(EXCLUDED.order_id, p.order_id),
		        charge_ref           = COALESCE(EXCLUDED.charge_ref, p.charge_ref),
		        provider_payment_ref = COALESCE(EXCLUDED.provider_payment_ref, p.provider_payment_ref),
		        currency             = CASE WHEN EXCLUDED.amount IS NOT NULL THEN EXCLUDED.currency ELSE p.currency END,
		        amount               = COALESCE(EXCLUDED.amount, p.amount),
		        reason               = CASE WHEN EXCLUDED.reason = '' THEN p.reason ELSE EXCLUDED.reason END,
		        status               = CASE WHEN EXCLUDED.status = '' THEN p.status ELSE EXCLUDED.status END,
		        evidence_due_by      = COALESCE(EXCLUDED.evidence_due_by, p.evidence_due_by),
		        is_charge_refundable = COALESCE(EXCLUDED.is_charge_refundable, p.is_charge_refundable),
		        updated_at           = now()
		  WHERE p.club_id = EXCLUDED.club_id
		    AND (`+newRank+` > `+oldRank+`
		         OR (`+newRank+` = `+oldRank+` AND (`+oldRank+` < 6 OR EXCLUDED.status = p.status))
		         OR (`+newRank+` = 0 AND `+oldRank+` < 6))`,
		d.ClubID, attempt, order, d.Provider, d.ProviderDisputeID, d.ChargeRef, d.PaymentRef,
		d.AmountOre, d.Currency, d.Reason, d.Status, d.EvidenceDueBy, d.ChargeRefundable,
	)
	if err != nil {
		return false, fmt.Errorf("storing dispute: %w", err)
	}

	// Claimed whether or not this event changed the row, so a delivery that
	// stored the dispute and then failed before telling is made up for by
	// the next one.
	err = q.QueryRow(ctx,
		`UPDATE payment_disputes SET notified_phase = `+disputePhaseSQL+`
		  WHERE club_id = $1 AND provider = $2 AND provider_dispute_id = $3
		    AND `+disputePhaseSQL+` > notified_phase
		 RETURNING true`,
		d.ClubID, d.Provider, d.ProviderDisputeID,
	).Scan(&notify)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claiming dispute notice: %w", err)
	}
	return notify, nil
}

// openDisputeSQL is the condition, on payment_disputes columns, for a
// dispute that stands in the way of refunding its payment; see
// OpenDisputeForAttempt for why each status counts.
const openDisputeSQL = `(is_charge_refundable = false
		         OR (is_charge_refundable IS NULL AND status IN ('needs_response', 'under_review', 'lost')))`

// OpenDispute is a dispute that stands in the way of refunding a payment.
type OpenDispute struct {
	ProviderDisputeID string
	Status            string
	EvidenceDueBy     *time.Time
}

// OpenDisputeForAttempt returns the dispute that makes a payment attempt
// impossible to refund, or nil when there is none. The provider's own answer
// decides when the event gave it (is_charge_refundable): false blocks, true
// does not. Without it, a chargeback awaiting an answer or under review
// blocks, and so does a lost one (the money is gone and the provider refuses
// the refund). An inquiry does not: refunding is how the club settles it. A
// dispute won, refunded, prevented or of a status this code does not know
// does not block either. The refund call itself must still handle the
// provider's refusal (Stripe's charge_disputed), since the state here can
// lag the provider's.
func OpenDisputeForAttempt(ctx context.Context, q Querier, clubID, attemptID string) (*OpenDispute, error) {
	var d OpenDispute
	err := q.QueryRow(ctx,
		`SELECT provider_dispute_id, status, evidence_due_by
		   FROM payment_disputes
		  WHERE club_id = $1 AND attempt_id = $2
		    AND `+openDisputeSQL+`
		  ORDER BY created_at, id LIMIT 1`,
		clubID, attemptID,
	).Scan(&d.ProviderDisputeID, &d.Status, &d.EvidenceDueBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading open dispute: %w", err)
	}
	return &d, nil
}
