package payments

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RefundAttemptView is one captured payment of an order and what can be done
// with it. A duplicate capture (a second payment of an order already paid) is
// its own attempt, refunded on its own.
type RefundAttemptView struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Currency string `json:"currency"`
	// Amount, Refunded and Refundable are in kr. Refunded counts requested
	// and succeeded refunds.
	Amount     float64 `json:"amount"`
	Refunded   float64 `json:"refunded"`
	Refundable float64 `json:"refundable"`
	// Duplicate marks a payment of an order that an earlier payment had
	// already paid.
	Duplicate bool `json:"duplicate"`
	Disputed  bool `json:"disputed"`
	// CanRefund is false for a provider that cannot refund, a disputed
	// payment and one with nothing left.
	CanRefund        bool `json:"can_refund"`
	RecollectAllowed bool `json:"recollect_allowed"`
	// PaymentURL links to the payment in the provider's dashboard; empty when
	// the provider's id for it is not known yet.
	PaymentURL string    `json:"payment_url,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// RefundDisputeView is a dispute against one of the order's payments.
type RefundDisputeView struct {
	ID            string     `json:"id"`
	AttemptID     *string    `json:"attempt_id"`
	Status        string     `json:"status"`
	Reason        string     `json:"reason"`
	Amount        *float64   `json:"amount"`
	EvidenceDueBy *time.Time `json:"evidence_due_by"`
	// Open is a dispute that stops a refund.
	Open bool `json:"open"`
}

// OrderRefunds is what the refund dialog and the order's refund history show.
type OrderRefunds struct {
	Attempts []RefundAttemptView `json:"attempts"`
	Refunds  []RefundRecord      `json:"refunds"`
	Disputes []RefundDisputeView `json:"disputes"`
	// HasFakturas is an order that pays fakturas, which a full refund can
	// credit or collect again.
	HasFakturas      bool `json:"has_fakturas"`
	CanRefund        bool `json:"can_refund"`
	RecollectAllowed bool `json:"recollect_allowed"`
}

// OrderRefundView reads an order's payments, refunds and disputes. paymentURL
// builds the link to a payment in the provider's dashboard from the
// provider's payment id (nil: no links).
func (s *RefundService) OrderRefundView(ctx context.Context, clubID, orderID string, paymentURL func(provider, paymentRef string) string) (*OrderRefunds, error) {
	if _, err := uuid.Parse(orderID); err != nil {
		return nil, ErrRefundNotFound
	}
	out := &OrderRefunds{Attempts: []RefundAttemptView{}, Refunds: []RefundRecord{}, Disputes: []RefundDisputeView{}}
	var hasCredit bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM order_sources WHERE order_id = o.id),
		        EXISTS (SELECT 1 FROM refund_documents rd JOIN order_sources os ON os.invoice_id = rd.invoice_id
		                 WHERE os.order_id = o.id AND rd.kind = 'credit')
		   FROM orders o WHERE o.id = $1 AND o.club_id = $2`, orderID, clubID,
	).Scan(&out.HasFakturas, &hasCredit)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRefundNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading order: %w", err)
	}

	if err := s.readAttempts(ctx, clubID, orderID, out, hasCredit, paymentURL); err != nil {
		return nil, err
	}
	if err := s.readRefunds(ctx, clubID, orderID, out); err != nil {
		return nil, err
	}
	if err := s.readDisputes(ctx, clubID, orderID, out); err != nil {
		return nil, err
	}
	for _, a := range out.Attempts {
		out.CanRefund = out.CanRefund || a.CanRefund
		out.RecollectAllowed = out.RecollectAllowed || a.RecollectAllowed
	}
	return out, nil
}

func (s *RefundService) readAttempts(ctx context.Context, clubID, orderID string, out *OrderRefunds, hasCredit bool, paymentURL func(provider, paymentRef string) string) error {
	rows, err := s.db.Query(ctx,
		`SELECT pa.id::text, pa.provider, pa.currency, round(pa.amount * 100)::bigint,
		        COALESCE((SELECT SUM(round(r.amount * 100)) FROM payment_refunds r
		                   WHERE r.attempt_id = pa.id AND r.status IN ('requested', 'succeeded')), 0)::bigint,
		        EXISTS (SELECT 1 FROM payment_disputes WHERE attempt_id = pa.id AND `+openDisputeSQL+`),
		        NOT EXISTS (SELECT 1 FROM payment_attempts o
		                     WHERE o.order_id = pa.order_id AND o.status = 'captured'
		                       AND (o.created_at, o.id) < (pa.created_at, pa.id)),
		        COALESCE((SELECT r.provider_payment_ref FROM payment_refunds r
		                   WHERE r.attempt_id = pa.id AND r.provider_payment_ref IS NOT NULL LIMIT 1),
		                 (SELECT t.payment_intent_id FROM stripe_balance_transactions t
		                   WHERE t.club_id = pa.club_id AND t.attempt_id = pa.id
		                     AND t.reporting_category = 'charge' AND t.payment_intent_id <> '' LIMIT 1), ''),
		        pa.created_at
		   FROM payment_attempts pa
		  WHERE pa.order_id = $1 AND pa.club_id = $2 AND pa.status = 'captured'
		  ORDER BY pa.created_at, pa.id`, orderID, clubID)
	if err != nil {
		return fmt.Errorf("reading payments: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a RefundAttemptView
		var amount, refunded int64
		var primary bool
		var ref string
		if err := rows.Scan(&a.ID, &a.Provider, &a.Currency, &amount, &refunded, &a.Disputed, &primary, &ref, &a.CreatedAt); err != nil {
			return fmt.Errorf("reading payment: %w", err)
		}
		a.Amount, a.Refunded, a.Refundable = float64(amount)/100, float64(refunded)/100, float64(amount-refunded)/100
		a.Duplicate = !primary
		_, canProvider := s.reg.RefunderFor(a.Provider)
		a.CanRefund = canProvider && !a.Disputed && amount-refunded > 0
		a.RecollectAllowed = a.CanRefund && out.HasFakturas && primary && refunded == 0 && !hasCredit
		if paymentURL != nil && ref != "" {
			a.PaymentURL = paymentURL(a.Provider, ref)
		}
		out.Attempts = append(out.Attempts, a)
	}
	return rows.Err()
}

func (s *RefundService) readRefunds(ctx context.Context, clubID, orderID string, out *OrderRefunds) error {
	rows, err := s.db.Query(ctx,
		`SELECT `+refundColumns+`
		   FROM payment_refunds r JOIN payment_attempts pa ON pa.id = r.attempt_id
		  WHERE pa.order_id = $1 AND r.club_id = $2
		  ORDER BY r.created_at, r.id`, orderID, clubID)
	if err != nil {
		return fmt.Errorf("reading refunds: %w", err)
	}
	refunds, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (RefundRecord, error) { return scanRefund(row) })
	if err != nil {
		return fmt.Errorf("reading refunds: %w", err)
	}
	out.Refunds = append(out.Refunds, refunds...)
	return nil
}

func (s *RefundService) readDisputes(ctx context.Context, clubID, orderID string, out *OrderRefunds) error {
	rows, err := s.db.Query(ctx,
		`SELECT provider_dispute_id, attempt_id::text, status, reason, amount::float8, evidence_due_by, `+openDisputeSQL+`
		   FROM payment_disputes
		  WHERE club_id = $1
		    AND (order_id = $2 OR attempt_id IN (SELECT id FROM payment_attempts WHERE order_id = $2))
		  ORDER BY created_at, id`, clubID, orderID)
	if err != nil {
		return fmt.Errorf("reading disputes: %w", err)
	}
	disputes, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (RefundDisputeView, error) {
		var d RefundDisputeView
		err := row.Scan(&d.ID, &d.AttemptID, &d.Status, &d.Reason, &d.Amount, &d.EvidenceDueBy, &d.Open)
		return d, err
	})
	if err != nil {
		return fmt.Errorf("reading disputes: %w", err)
	}
	out.Disputes = append(out.Disputes, disputes...)
	return nil
}

// RefundableOrders returns which of the given orders can be refunded now: a
// captured payment of a provider that refunds, with money left and no open
// dispute. The lists that show a refund button use it for a page of rows at
// once.
func (s *RefundService) RefundableOrders(ctx context.Context, clubID string, orderIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	providers := s.reg.RefundProviders()
	if len(orderIDs) == 0 || len(providers) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT DISTINCT pa.order_id::text
		   FROM payment_attempts pa
		  WHERE pa.club_id = $1 AND pa.order_id = ANY($2::uuid[]) AND pa.status = 'captured'
		    AND pa.provider = ANY($3::text[])
		    AND round(pa.amount * 100)::bigint >
		        COALESCE((SELECT SUM(round(r.amount * 100)) FROM payment_refunds r
		                   WHERE r.attempt_id = pa.id AND r.status IN ('requested', 'succeeded')), 0)
		    AND NOT EXISTS (SELECT 1 FROM payment_disputes WHERE attempt_id = pa.id AND `+openDisputeSQL+`)`,
		clubID, orderIDs, providers)
	if err != nil {
		return nil, fmt.Errorf("finding refundable orders: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("finding refundable orders: %w", err)
	}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}
