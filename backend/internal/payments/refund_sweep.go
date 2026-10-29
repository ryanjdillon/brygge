package payments

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// RefundSweepResult counts what a sweep did.
type RefundSweepResult struct {
	Checked int
	// Settled are rows the provider had a refund for, whatever its state.
	Settled int
	// Resent are requests the provider had nothing for, sent again under
	// their own key.
	Resent int
	// Failed are rows given up on: the provider never made the refund.
	Failed int
	Errors int
}

// SweepRequested settles the club's refunds that have been requested for more
// than ten minutes: the provider is asked what it made for each request key
// (FindRefund). A request it has nothing for is sent again under the same
// key while the key is young (under 23 hours), since the request may never
// have left; it is never sent under a new key. After 24 hours with nothing
// the request is marked failed, since the provider's Idempotency-Key has
// expired and the refund will not appear (PAY-12).
func (s *RefundService) SweepRequested(ctx context.Context, clubID string) (RefundSweepResult, error) {
	return s.sweep(ctx, clubID, "")
}

// SweepOrder is SweepRequested for the refunds of one order, for when its
// refund list is read.
func (s *RefundService) SweepOrder(ctx context.Context, clubID, orderID string) (RefundSweepResult, error) {
	return s.sweep(ctx, clubID, orderID)
}

type staleRefund struct {
	attempt refundAttempt
	rec     RefundRecord
}

func (s *RefundService) sweep(ctx context.Context, clubID, orderID string) (RefundSweepResult, error) {
	var res RefundSweepResult
	rows, err := s.db.Query(ctx,
		`SELECT `+refundColumns+`, COALESCE(pa.provider_reference, ''), pa.order_id::text
		   FROM payment_refunds r JOIN payment_attempts pa ON pa.id = r.attempt_id
		  WHERE r.club_id = $1 AND r.status = 'requested' AND r.request_key IS NOT NULL
		    AND r.created_at < $2 AND ($3 = '' OR pa.order_id::text = $3)
		  ORDER BY r.created_at`,
		clubID, s.now().Add(-refundStaleAfter), orderID)
	if err != nil {
		return res, fmt.Errorf("listing requested refunds: %w", err)
	}
	stale, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (staleRefund, error) {
		var r staleRefund
		r.attempt = refundAttempt{clubID: clubID}
		err := row.Scan(&r.rec.ID, &r.rec.AttemptID, &r.rec.Provider, &r.rec.AmountOre, &r.rec.Currency,
			&r.rec.Status, &r.rec.Reason, &r.rec.Outcome, &r.rec.FailureReason, &r.rec.ProviderRefundID, &r.rec.CreatedBy,
			&r.rec.RequestKey, &r.rec.CreatedAt, &r.rec.UpdatedAt, &r.attempt.reference, &r.attempt.orderID)
		r.attempt.id, r.attempt.provider, r.attempt.currency = r.rec.AttemptID, r.rec.Provider, r.rec.Currency
		return r, err
	})
	if err != nil {
		return res, fmt.Errorf("reading requested refunds: %w", err)
	}
	for _, r := range stale {
		s.sweepOne(ctx, r, &res)
	}
	return res, nil
}

func (s *RefundService) sweepOne(ctx context.Context, r staleRefund, res *RefundSweepResult) {
	ref, ok := s.reg.RefunderFor(r.attempt.provider)
	if !ok {
		return
	}
	res.Checked++
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refundCallTimeout)
	defer cancel()
	_, settled, answered := s.lookup(ctx, ref, r.attempt, r.rec)
	age := s.now().Sub(r.rec.CreatedAt)
	switch {
	case !answered:
		res.Errors++
	case settled:
		res.Settled++
	case r.rec.ProviderRefundID != nil:
		// Stripe has an id for it but lists nothing under the key: leave it
		// to the webhook rather than send anything.
	case age < refundResendWindow:
		res.Resent++
		if sent := s.send(ctx, ref, r.attempt, r.rec); sent.Rejected != nil {
			res.Failed++
		}
	case age > refundGiveUpAfter:
		if _, err := markFailed(ctx, s.db, r.attempt.clubID, r.rec.ID, "not made by the provider"); err != nil {
			res.Errors++
			s.log.Error().Err(err).Str("refund_id", r.rec.ID).Msg("refund sweep: marking the refund failed failed")
			return
		}
		res.Failed++
	}
}
