package payments

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// payment_refunds holds the state of every refund a provider made, whoever
// asked for it: the refund button writes a row before it calls the provider,
// and the provider's webhook writes one for a refund made in its dashboard.
// Neither writes anything else. The refund is booked, and its credit note
// issued, from the provider's balance by the booking pass (accounting),
// which finds this row by the provider's refund id or the request key.

// Refund outcomes the button can ask for on a full refund of a faktura:
// credit it, or collect it again. NULL means credit.
const (
	RefundOutcomeCredit    = "credit"
	RefundOutcomeRecollect = "recollect"
)

// DashboardRefundReason is the reason stored on a refund Brygge learns of
// from the provider rather than from a person.
const DashboardRefundReason = "Stripe Dashboard"

// RefundRecord is one payment_refunds row.
type RefundRecord struct {
	ID        string `json:"id"`
	AttemptID string `json:"attempt_id"`
	Provider  string `json:"provider"`
	// AmountOre is the refund in minor units; the wire form is Amount in kr.
	AmountOre        int64     `json:"-"`
	Amount           float64   `json:"amount"`
	Currency         string    `json:"currency"`
	Status           string    `json:"status"`
	Reason           string    `json:"reason"`
	Outcome          *string   `json:"outcome"`
	FailureReason    *string   `json:"failure_reason"`
	ProviderRefundID *string   `json:"provider_refund_id"`
	CreatedBy        *string   `json:"created_by"`
	RequestKey       *string   `json:"request_key"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// refundColumns is the select list scanRefund reads, over payment_refunds
// aliased r.
const refundColumns = `r.id::text, r.attempt_id::text, r.provider, round(r.amount * 100)::bigint, r.currency,
	r.status, r.reason, r.outcome, r.failure_reason, r.provider_refund_id, r.created_by::text,
	r.request_key::text, r.created_at, r.updated_at`

func scanRefund(row pgx.Row) (RefundRecord, error) {
	var r RefundRecord
	err := row.Scan(&r.ID, &r.AttemptID, &r.Provider, &r.AmountOre, &r.Currency,
		&r.Status, &r.Reason, &r.Outcome, &r.FailureReason, &r.ProviderRefundID, &r.CreatedBy,
		&r.RequestKey, &r.CreatedAt, &r.UpdatedAt)
	r.Amount = float64(r.AmountOre) / 100
	return r, err
}

// ProviderRefund is a refund as the provider's own records have it.
type ProviderRefund struct {
	ClubID, Provider, AttemptID string
	ProviderRefundID            string
	AmountOre                   int64
	Currency                    string
	// Status is Brygge's (RefundRequested, RefundSucceeded, RefundFailed).
	Status        string
	FailureReason string
	PaymentRef    string
	ChargeRef     string
	// RequestKey is the key a refund made by the button carries in the
	// provider's metadata; empty for one made elsewhere or not a UUID.
	RequestKey string
}

// stateUpdateSQL sets the provider-side fields of a refund row from $3..$6:
// status, failure reason, payment and charge refs. A finished refund is never
// put back to requested by an update that raced a newer one.
const stateUpdateSQL = `
	status               = CASE WHEN $3 = 'requested' AND payment_refunds.status IN ('succeeded', 'failed')
	                            THEN payment_refunds.status ELSE $3 END,
	failure_reason       = CASE WHEN $3 = 'failed' THEN NULLIF($4, '')
	                            WHEN $3 = 'requested' AND payment_refunds.status = 'failed' THEN payment_refunds.failure_reason
	                            ELSE NULL END,
	provider_payment_ref = COALESCE(NULLIF($5, ''), payment_refunds.provider_payment_ref),
	charge_ref           = COALESCE(NULLIF($6, ''), payment_refunds.charge_ref),
	updated_at           = now()`

// UpsertProviderRefund records a refund the provider reports. The row is
// found by the provider's refund id, else by the request key a button refund
// carries (and learns its refund id then); a refund neither finds was made
// in the provider's dashboard and gets a row with no person and the reason
// DashboardRefundReason. It is idempotent under redelivery and keeps what a
// person entered (reason, outcome, who).
func UpsertProviderRefund(ctx context.Context, q Querier, r ProviderRefund) error {
	if r.ClubID == "" || r.Provider == "" || r.AttemptID == "" || r.ProviderRefundID == "" {
		return errors.New("a provider refund needs a club, a provider, an attempt and a refund id")
	}
	if r.AmountOre <= 0 {
		return fmt.Errorf("refund %s has no amount", r.ProviderRefundID)
	}
	tag, err := q.Exec(ctx,
		`UPDATE payment_refunds SET `+stateUpdateSQL+`
		  WHERE club_id = $1 AND provider = $2 AND provider_refund_id = $7`,
		r.ClubID, r.Provider, r.Status, r.FailureReason, r.PaymentRef, r.ChargeRef, r.ProviderRefundID)
	if err != nil {
		return fmt.Errorf("updating refund: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}

	var key *string
	if _, err := uuid.Parse(r.RequestKey); err == nil {
		key = &r.RequestKey
		tag, err = q.Exec(ctx,
			`UPDATE payment_refunds SET provider_refund_id = $8, `+stateUpdateSQL+`
			  WHERE club_id = $1 AND provider = $2 AND request_key = $7::uuid AND provider_refund_id IS NULL`,
			r.ClubID, r.Provider, r.Status, r.FailureReason, r.PaymentRef, r.ChargeRef, r.RequestKey, r.ProviderRefundID)
		if err != nil {
			return fmt.Errorf("matching refund by request key: %w", err)
		}
		if tag.RowsAffected() > 0 {
			return nil
		}
	}

	// A refund made in the dashboard, or one whose request row is gone. The
	// key is kept only when no row holds it (its unique index would refuse a
	// second).
	_, err = q.Exec(ctx,
		`INSERT INTO payment_refunds AS pr
		     (club_id, attempt_id, provider, amount, currency, provider_refund_id, status, reason,
		      failure_reason, provider_payment_ref, charge_ref, request_key)
		 VALUES ($1, $2, $3, $4::bigint / 100.0, COALESCE(NULLIF(upper($5), ''), 'NOK'), $6, $7, $8,
		         NULLIF($9, ''), NULLIF($10, ''), NULLIF($11, ''),
		         CASE WHEN EXISTS (SELECT 1 FROM payment_refunds WHERE club_id = $1 AND request_key = $12::uuid)
		              THEN NULL ELSE $12::uuid END)
		 ON CONFLICT (provider, provider_refund_id) WHERE provider_refund_id IS NOT NULL DO UPDATE
		    SET status = CASE WHEN EXCLUDED.status = 'requested' AND pr.status IN ('succeeded', 'failed')
		                      THEN pr.status ELSE EXCLUDED.status END,
		        failure_reason = CASE WHEN EXCLUDED.status = 'failed' THEN EXCLUDED.failure_reason
		                              WHEN EXCLUDED.status = 'requested' AND pr.status = 'failed' THEN pr.failure_reason
		                              ELSE NULL END,
		        provider_payment_ref = COALESCE(EXCLUDED.provider_payment_ref, pr.provider_payment_ref),
		        charge_ref = COALESCE(EXCLUDED.charge_ref, pr.charge_ref),
		        updated_at = now()
		  WHERE pr.club_id = EXCLUDED.club_id`,
		r.ClubID, r.AttemptID, r.Provider, r.AmountOre, r.Currency, r.ProviderRefundID, r.Status,
		DashboardRefundReason, r.FailureReason, r.PaymentRef, r.ChargeRef, key)
	if err != nil {
		return fmt.Errorf("inserting refund: %w", err)
	}
	return nil
}

// applyResult stores what the provider answered for a request-key row: its
// refund id, refs and status. Only a row still requested takes the status,
// so a slower answer never undoes what the webhook already settled.
func applyResult(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, clubID, id string, res RefundResult) (RefundRecord, error) {
	return scanRefund(q.QueryRow(ctx,
		`UPDATE payment_refunds r SET
		        provider_refund_id   = COALESCE(r.provider_refund_id, NULLIF($3, '')),
		        provider_payment_ref = COALESCE(r.provider_payment_ref, NULLIF($4, '')),
		        charge_ref           = COALESCE(r.charge_ref, NULLIF($5, '')),
		        status               = CASE WHEN r.status = 'requested' THEN $6 ELSE r.status END,
		        failure_reason       = CASE WHEN r.status = 'requested' AND $6 = 'failed' THEN NULLIF($7, '')
		                                    ELSE r.failure_reason END,
		        updated_at           = now()
		  WHERE r.id = $1 AND r.club_id = $2
		  RETURNING `+refundColumns,
		id, clubID, res.ProviderRefundID, res.PaymentRef, res.ChargeRef, res.Status, res.FailureReason))
}

// markFailed records that the provider certainly did not make a refund. A
// row that is no longer requested (the webhook found the refund after all)
// is left as it is.
func markFailed(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, clubID, id, reason string) (RefundRecord, error) {
	rec, err := scanRefund(q.QueryRow(ctx,
		`UPDATE payment_refunds r SET status = 'failed', failure_reason = NULLIF($3, ''), updated_at = now()
		  WHERE r.id = $1 AND r.club_id = $2 AND r.status = 'requested'
		  RETURNING `+refundColumns, id, clubID, reason))
	if errors.Is(err, pgx.ErrNoRows) {
		return scanRefund(q.QueryRow(ctx,
			`SELECT `+refundColumns+` FROM payment_refunds r WHERE r.id = $1 AND r.club_id = $2`, id, clubID))
	}
	return rec, err
}
