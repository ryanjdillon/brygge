package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// Refund refusal codes (409). The refund endpoint answers them with the
// request left unrecorded.
const (
	RefundNotSupported         = "not_supported"
	RefundNotCaptured          = "not_captured"
	RefundDisputed             = "disputed"
	RefundAmountExceeds        = "amount_exceeds_refundable"
	RefundRecollectAfterPartly = "recollect_after_partial"
	RefundRequestKeyReused     = "request_key_reused"
)

// MaxRefundReasonRunes is the longest reason a person may give a refund.
const MaxRefundReasonRunes = 500

// RefundRefusal is a refund the system will not start: the order, payment or
// request is in a state that rules it out.
type RefundRefusal struct {
	Code    string
	Message string
}

func (e *RefundRefusal) Error() string { return e.Message }

var (
	// ErrRefundNotFound is an order, or a captured attempt of it, that the
	// club does not have.
	ErrRefundNotFound = errors.New("order or payment not found")
	// ErrRefundInvalid wraps a request that is wrong whatever the state.
	ErrRefundInvalid = errors.New("invalid refund request")
)

const (
	// refundStaleAfter is how long a requested refund waits before the sweep
	// asks the provider what became of it.
	refundStaleAfter = 10 * time.Minute
	// refundGiveUpAfter is how long a request the provider has no refund for
	// is kept: Stripe forgets an Idempotency-Key after 24 hours, so a refund
	// it never made by then will not appear.
	refundGiveUpAfter = 24 * time.Hour
	// refundResendWindow is how long after it was stored a request the
	// provider has no refund for may be sent again, under the same key: the
	// provider's idempotency makes that safe while it still remembers the key,
	// so a request that never left (a crash between the commit and the call)
	// does not hold its amount for a day. Past it only the sweep's give-up
	// remains, a margin under refundGiveUpAfter.
	refundResendWindow = 23 * time.Hour
	// refundCallTimeout bounds the provider calls made after the request is
	// stored, which outlive a client that gave up waiting.
	refundCallTimeout = 45 * time.Second
	// refundLookupTimeout bounds a FindRefund, which has its own budget so a
	// slow refund call cannot starve the look-up that follows it.
	refundLookupTimeout = 10 * time.Second
)

// RefundService starts refunds of captured payments and keeps their state.
// It books nothing: the booking pass does, from the provider's balance.
type RefundService struct {
	db  *pgxpool.Pool
	reg *Registry
	log zerolog.Logger
	now func() time.Time
}

// NewRefundService returns the service over the registry of providers that
// may refund.
func NewRefundService(db *pgxpool.Pool, reg *Registry, log zerolog.Logger) *RefundService {
	return &RefundService{db: db, reg: reg, log: log.With().Str("component", "refunds").Logger(), now: time.Now}
}

// RefundInput is one request to refund part or all of an attempt.
type RefundInput struct {
	ClubID, OrderID, AttemptID string
	// AmountOre is the refund in minor units; 0 means all that is refundable.
	AmountOre int64
	Reason    string
	// Outcome is RefundOutcomeCredit or RefundOutcomeRecollect for a full
	// refund of an order with fakturas, else empty.
	Outcome string
	// RequestKey identifies the click. Repeating it never refunds twice.
	RequestKey string
	ActorID    string
}

// RefundResponse is what a refund request came to.
type RefundResponse struct {
	Refund RefundRecord
	// Replayed is a request key that was used before: nothing new was started.
	Replayed bool
	// Awaiting means the provider has not confirmed the refund yet: the call's
	// outcome is unknown, or the provider still has it pending.
	Awaiting bool
	// Rejected is set when the provider refused the refund; Refund is failed.
	Rejected *RefundRejectedError
}

type refundAttempt struct {
	clubID, id, orderID, provider, reference, currency string
	status                                             string
	amountOre                                          int64
	primary                                            bool
}

// Request starts a refund. The request is stored first, under the order's
// and the attempt's locks, so two requests over the same money cannot both
// pass; the provider is called after the lock is released. A definite refusal
// from the provider marks the row failed; an uncertain one leaves it
// requested for FindRefund (here once, then the sweep) to settle; a request
// the provider has nothing for is sent again under the same key for a day
// (PAY-12). A refund is never sent under a new key.
func (s *RefundService) Request(ctx context.Context, in RefundInput) (*RefundResponse, error) {
	if err := validateRefundInput(&in); err != nil {
		return nil, err
	}
	att, rec, replay, err := s.reserve(ctx, in)
	if err != nil {
		return nil, err
	}
	ref, ok := s.reg.RefunderFor(att.provider)
	if !ok {
		return nil, &RefundRefusal{Code: RefundNotSupported, Message: "the payment provider cannot refund"}
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refundCallTimeout)
	defer cancel()
	if replay {
		out := s.resume(ctx, ref, att, rec)
		out.Replayed = true
		return out, nil
	}
	return s.send(ctx, ref, att, rec), nil
}

func validateRefundInput(in *RefundInput) error {
	in.Reason = strings.TrimSpace(in.Reason)
	switch {
	case in.Reason == "":
		return fmt.Errorf("%w: a reason is required", ErrRefundInvalid)
	case utf8.RuneCountInString(in.Reason) > MaxRefundReasonRunes:
		return fmt.Errorf("%w: the reason is longer than %d characters", ErrRefundInvalid, MaxRefundReasonRunes)
	case in.AmountOre < 0:
		return fmt.Errorf("%w: the amount must be positive", ErrRefundInvalid)
	case in.Outcome != "" && in.Outcome != RefundOutcomeCredit && in.Outcome != RefundOutcomeRecollect:
		return fmt.Errorf("%w: outcome must be credit or recollect", ErrRefundInvalid)
	}
	if _, err := uuid.Parse(in.RequestKey); err != nil {
		return fmt.Errorf("%w: request_key must be a uuid", ErrRefundInvalid)
	}
	for _, id := range []string{in.OrderID, in.AttemptID} {
		if _, err := uuid.Parse(id); err != nil {
			return ErrRefundNotFound
		}
	}
	return nil
}

// reserve is the first transaction: lock the order, then the attempt, decide
// whether the refund may start and store it as requested.
func (s *RefundService) reserve(ctx context.Context, in RefundInput) (att refundAttempt, rec RefundRecord, replay bool, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return att, rec, false, fmt.Errorf("beginning refund: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if att, err = lockRefundAttempt(ctx, tx, in); err != nil {
		return att, rec, false, err
	}
	existing, err := scanRefund(tx.QueryRow(ctx,
		`SELECT `+refundColumns+` FROM payment_refunds r WHERE r.club_id = $1 AND r.request_key = $2::uuid`,
		in.ClubID, in.RequestKey))
	switch {
	case err == nil:
		if existing.AttemptID != in.AttemptID || (in.AmountOre != 0 && in.AmountOre != existing.AmountOre) ||
			in.Outcome != derefOr(existing.Outcome) || in.Reason != existing.Reason {
			return att, rec, false, requestKeyReused()
		}
		return att, existing, true, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return att, rec, false, fmt.Errorf("finding refund request: %w", err)
	}

	amountOre, err := s.checkRefundable(ctx, tx, in, att)
	if err != nil {
		return att, rec, false, err
	}
	rec, err = scanRefund(tx.QueryRow(ctx,
		`INSERT INTO payment_refunds AS r (club_id, attempt_id, provider, amount, currency, status, reason,
		                                created_by, request_key, outcome)
		 VALUES ($1, $2, $3, $4::bigint / 100.0, $5, 'requested', $6, $7, $8::uuid, NULLIF($9, ''))
		 RETURNING `+refundColumns,
		in.ClubID, att.id, att.provider, amountOre, att.currency, in.Reason, in.ActorID, in.RequestKey, in.Outcome))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return att, rec, false, requestKeyReused()
		}
		return att, rec, false, fmt.Errorf("storing refund request: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return att, rec, false, fmt.Errorf("committing refund request: %w", err)
	}
	return att, rec, false, nil
}

func requestKeyReused() error {
	return &RefundRefusal{Code: RefundRequestKeyReused, Message: "the request key was used for another refund"}
}

// lockRefundAttempt locks the order, then its attempt, and reads the attempt.
func lockRefundAttempt(ctx context.Context, tx pgx.Tx, in RefundInput) (refundAttempt, error) {
	var locked string
	err := tx.QueryRow(ctx,
		`SELECT id::text FROM orders WHERE id = $1 AND club_id = $2 FOR UPDATE`, in.OrderID, in.ClubID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return refundAttempt{}, ErrRefundNotFound
	}
	if err != nil {
		return refundAttempt{}, fmt.Errorf("locking order: %w", err)
	}
	a := refundAttempt{clubID: in.ClubID, id: in.AttemptID, orderID: in.OrderID}
	err = tx.QueryRow(ctx,
		`SELECT pa.provider, COALESCE(pa.provider_reference, ''), pa.status, round(pa.amount * 100)::bigint, pa.currency,
		        NOT EXISTS (SELECT 1 FROM payment_attempts o
		                     WHERE o.order_id = pa.order_id AND o.status = 'captured'
		                       AND (o.created_at, o.id) < (pa.created_at, pa.id))
		   FROM payment_attempts pa
		  WHERE pa.id = $1 AND pa.order_id = $2 AND pa.club_id = $3
		    FOR UPDATE OF pa`,
		in.AttemptID, in.OrderID, in.ClubID,
	).Scan(&a.provider, &a.reference, &a.status, &a.amountOre, &a.currency, &a.primary)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrRefundNotFound
	}
	if err != nil {
		return a, fmt.Errorf("locking payment attempt: %w", err)
	}
	return a, nil
}

// checkRefundable applies the refusals and returns the amount to refund.
func (s *RefundService) checkRefundable(ctx context.Context, tx pgx.Tx, in RefundInput, a refundAttempt) (int64, error) {
	if a.status != "captured" {
		return 0, &RefundRefusal{Code: RefundNotCaptured, Message: "the payment was not captured"}
	}
	if _, ok := s.reg.RefunderFor(a.provider); !ok {
		return 0, &RefundRefusal{Code: RefundNotSupported, Message: "the payment provider cannot refund"}
	}
	dispute, err := OpenDisputeForAttempt(ctx, tx, in.ClubID, a.id)
	if err != nil {
		return 0, err
	}
	if dispute != nil {
		return 0, &RefundRefusal{Code: RefundDisputed, Message: "the payment is disputed"}
	}

	var otherOre int64
	var otherCount int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(round(amount * 100)), 0)::bigint, count(*)
		   FROM payment_refunds WHERE attempt_id = $1 AND status IN ('requested', 'succeeded')`, a.id,
	).Scan(&otherOre, &otherCount); err != nil {
		return 0, fmt.Errorf("summing refunds: %w", err)
	}
	refundable := a.amountOre - otherOre
	amount := in.AmountOre
	if amount == 0 {
		amount = refundable
	}
	if amount <= 0 || amount > refundable {
		return 0, &RefundRefusal{Code: RefundAmountExceeds, Message: "the amount is more than what is left to refund"}
	}
	if in.Outcome == "" {
		return amount, nil
	}
	return amount, s.checkOutcome(ctx, tx, in, a, amount == refundable, otherCount)
}

// checkOutcome rules on credit and recollect: they apply to the refund that
// returns the whole payment of an order with fakturas, and a recollect only
// when nothing was refunded of it before.
func (s *RefundService) checkOutcome(ctx context.Context, tx pgx.Tx, in RefundInput, a refundAttempt, completes bool, others int) error {
	var hasFakturas, hasCredit bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM order_sources WHERE order_id = $1),
		        EXISTS (SELECT 1 FROM refund_documents rd JOIN order_sources os ON os.invoice_id = rd.invoice_id
		                 WHERE os.order_id = $1 AND rd.kind = 'credit')`, a.orderID,
	).Scan(&hasFakturas, &hasCredit); err != nil {
		return fmt.Errorf("reading order fakturas: %w", err)
	}
	if !hasFakturas || !completes || !a.primary {
		return fmt.Errorf("%w: an outcome applies only to a full refund of a faktura's payment", ErrRefundInvalid)
	}
	if in.Outcome == RefundOutcomeRecollect && (others > 0 || hasCredit) {
		return &RefundRefusal{Code: RefundRecollectAfterPartly, Message: "a faktura cannot be collected again after part of it was refunded"}
	}
	return nil
}

// send calls the provider for a stored request and settles the row. It is
// the first call for a new request, and the same call under the same key for
// one that never got an answer.
func (s *RefundService) send(ctx context.Context, ref Refunder, a refundAttempt, rec RefundRecord) *RefundResponse {
	res, err := ref.Refund(ctx, RefundRequest{
		Reference: a.reference, AmountOre: rec.AmountOre, Currency: a.currency,
		RequestKey: derefOr(rec.RequestKey), OrderID: a.orderID, AttemptID: a.id,
	})
	if err == nil {
		return s.settled(ctx, a, rec, res)
	}
	if rej, ok := IsRefundRejected(err); ok {
		failed, mErr := markFailed(ctx, s.db, a.clubID, rec.ID, rejectReason(rej))
		if mErr != nil {
			s.log.Error().Err(mErr).Str("refund_id", rec.ID).Msg("marking refund failed")
			failed = rec
		}
		return &RefundResponse{Refund: failed, Rejected: rej}
	}
	// The refund may exist: look for it once, then leave it to the sweep.
	s.log.Warn().Err(err).Str("refund_id", rec.ID).Msg("refund outcome unknown, looking for it")
	if out, settled, _ := s.lookup(ctx, ref, a, rec); settled {
		return out
	}
	return &RefundResponse{Refund: rec, Awaiting: true}
}

// lookup asks the provider for the refund made for the row's request key and
// settles the row when it has one. answered is false when the look-up
// failed: that is no answer, and the caller must not take it for "the
// provider has nothing".
func (s *RefundService) lookup(ctx context.Context, ref Refunder, a refundAttempt, rec RefundRecord) (out *RefundResponse, settled, answered bool) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refundLookupTimeout)
	defer cancel()
	res, found, err := ref.FindRefund(ctx, a.reference, derefOr(rec.RequestKey))
	if err != nil {
		s.log.Warn().Err(err).Str("refund_id", rec.ID).Msg("looking for refund failed")
		return nil, false, false
	}
	if !found {
		return nil, false, true
	}
	return s.settled(ctx, a, rec, res), true, true
}

// resume learns what became of a request that is not settled. The provider
// has the refund: the row takes its state. It has none and the key is young
// enough, the request never left or was lost: it is sent again under the same
// key, which cannot refund twice. A look-up that failed, or a row the
// provider already has an id for, sends nothing.
func (s *RefundService) resume(ctx context.Context, ref Refunder, a refundAttempt, rec RefundRecord) *RefundResponse {
	if rec.Status != RefundRequested {
		return &RefundResponse{Refund: rec}
	}
	out, settled, answered := s.lookup(ctx, ref, a, rec)
	switch {
	case settled:
		return out
	case answered && rec.ProviderRefundID == nil && s.now().Sub(rec.CreatedAt) < refundResendWindow:
		return s.send(ctx, ref, a, rec)
	}
	return &RefundResponse{Refund: rec, Awaiting: true}
}

func (s *RefundService) settled(ctx context.Context, a refundAttempt, rec RefundRecord, res RefundResult) *RefundResponse {
	updated, err := applyResult(ctx, s.db, a.clubID, rec.ID, res)
	if err != nil {
		s.log.Error().Err(err).Str("refund_id", rec.ID).Msg("storing refund result")
		return &RefundResponse{Refund: rec, Awaiting: true}
	}
	return &RefundResponse{Refund: updated, Awaiting: updated.Status == RefundRequested}
}

func rejectReason(r *RefundRejectedError) string {
	if r.Code != "" {
		return r.Code
	}
	return r.Message
}

func derefOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
