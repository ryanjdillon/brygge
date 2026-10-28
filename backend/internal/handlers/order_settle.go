package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/vipps"
)

// orderSettlement records a payment provider's verdict on one payment attempt
// and, through it, on the checkout order the attempt pays. It is the only code
// that marks an order paid or failed, and it is called only from a provider's
// signature-verified webhook (see the "Only the payment provider can mark an
// order paid" invariant). Every write is guarded so a redelivered or
// out-of-order event is a no-op. The writes are best-effort and each runs on
// its own, so one failing does not undo the others: the event is already
// persisted and the provider redelivers it.
//
// An order can carry several attempts (a payer who abandons one clicks again
// and gets a fresh one). The newest is the order's current attempt; an older
// one can still be completed by the payer, so its capture still pays the
// order.
type orderSettlement struct {
	db  *pgxpool.Pool
	log zerolog.Logger
}

// paymentAttempt is a payment_attempts row as settlement needs it.
type paymentAttempt struct {
	ID       string
	OrderID  string
	Provider string
	// Status is the attempt's status when it was looked up.
	Status string
	// Amount and Currency are what the attempt was created to charge.
	Amount   float64
	Currency string
	// Guest marks an order with no linked member.
	Guest bool
}

// attemptColumns are scanned by scanAttempt, from payment_attempts pa joined
// to orders o.
const attemptColumns = `pa.id, pa.order_id, pa.status, pa.amount::float8, pa.currency, o.user_id IS NULL`

func scanAttempt(row pgx.Row, provider string) (paymentAttempt, bool) {
	a := paymentAttempt{Provider: provider}
	if err := row.Scan(&a.ID, &a.OrderID, &a.Status, &a.Amount, &a.Currency, &a.Guest); err != nil {
		return paymentAttempt{}, false
	}
	return a, true
}

// attemptByReference resolves a provider's reference to its attempt in the
// club, and whether one was found.
func (s orderSettlement) attemptByReference(ctx context.Context, clubID, provider, reference string) (paymentAttempt, bool) {
	return scanAttempt(s.db.QueryRow(ctx,
		`SELECT `+attemptColumns+`
		   FROM payment_attempts pa
		   JOIN orders o ON o.id = pa.order_id
		  WHERE pa.club_id = $1 AND pa.provider = $2 AND pa.provider_reference = $3`,
		clubID, provider, reference,
	), provider)
}

// attemptByID resolves an attempt id a provider echoed back (from metadata
// Brygge set) to the attempt in the club, and whether one was found. An id
// that is not a UUID matches nothing rather than failing the query.
func (s orderSettlement) attemptByID(ctx context.Context, clubID, provider, id string) (paymentAttempt, bool) {
	if _, err := uuid.Parse(id); err != nil {
		return paymentAttempt{}, false
	}
	return scanAttempt(s.db.QueryRow(ctx,
		`SELECT `+attemptColumns+`
		   FROM payment_attempts pa
		   JOIN orders o ON o.id = pa.order_id
		  WHERE pa.club_id = $1 AND pa.provider = $2 AND pa.id = $3`,
		clubID, provider, id,
	), provider)
}

// authDecision is what to do with an authorized payment.
type authDecision int

const (
	// authCapture: take the money.
	authCapture authDecision = iota
	// authSkip: a replay for an attempt already captured or voided.
	authSkip
	// authCancelOrderSettled: another attempt already paid the order.
	authCancelOrderSettled
	// authCancelSibling: another attempt on the order is already authorized
	// or captured, and only one may take the money.
	authCancelSibling
)

// authorizeAttempt decides what to do with an authorization and records the
// answer on the attempt: authorized for capture, cancelled for a void. The
// decision and the write share one transaction holding the order row, so of
// two attempts authorized at once only the first is captured; the other is
// voided. Recording the cancel before the provider is called means no later
// event can read the attempt as still payable; the void itself is
// best-effort, and an authorization whose void fails lapses at the provider.
// A failed decision answers capture, so a transient error never strands a
// payer's authorization; a resulting double capture is logged on CAPTURED.
func (s orderSettlement) authorizeAttempt(ctx context.Context, a paymentAttempt) authDecision {
	d, err := s.decideAuthorization(ctx, a)
	if err != nil {
		s.log.Error().Err(err).Str("attempt_id", a.ID).Msg("attempt authorized: decision failed, capturing")
		return authCapture
	}
	return d
}

// decideAuthorization runs entirely on its transaction: it holds the order's
// row lock and must not touch another pool connection or the provider.
func (s orderSettlement) decideAuthorization(ctx context.Context, a paymentAttempt) (authDecision, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return authCapture, err
	}
	defer tx.Rollback(ctx)

	var orderStatus, own string
	var sibling bool
	if err := tx.QueryRow(ctx,
		`SELECT status::text FROM orders WHERE id = $1 FOR UPDATE`, a.OrderID,
	).Scan(&orderStatus); err != nil {
		return authCapture, err
	}
	if err := tx.QueryRow(ctx,
		`SELECT pa.status,
		        EXISTS (SELECT 1 FROM payment_attempts o
		                 WHERE o.order_id = pa.order_id AND o.id <> pa.id
		                   AND o.status IN ('authorized', 'captured'))
		   FROM payment_attempts pa WHERE pa.id = $1`, a.ID,
	).Scan(&own, &sibling); err != nil {
		return authCapture, err
	}
	d, next := authCapture, "authorized"
	switch {
	case own == "captured" || own == "cancelled":
		return authSkip, nil
	case orderStatus == "paid" || orderStatus == "refunded":
		d, next = authCancelOrderSettled, "cancelled"
	case sibling:
		d, next = authCancelSibling, "cancelled"
	}
	if _, err := tx.Exec(ctx,
		`UPDATE payment_attempts SET status = $2, updated_at = now()
		  WHERE id = $1 AND status IN ('created', 'authorized', 'failed')`,
		a.ID, next,
	); err != nil {
		return authCapture, err
	}
	return d, tx.Commit(ctx)
}

// releaseAuthorization returns an attempt whose capture failed to created. An
// attempt left authorized would make every later attempt on the order be
// voided, so a payer who retries could never pay; the uncaptured reservation
// lapses at the provider.
func (s orderSettlement) releaseAuthorization(ctx context.Context, a paymentAttempt) {
	if _, err := s.db.Exec(ctx,
		`UPDATE payment_attempts SET status = 'created', updated_at = now()
		  WHERE id = $1 AND status = 'authorized'`,
		a.ID,
	); err != nil {
		s.log.Error().Err(err).Str("attempt_id", a.ID).Msg("attempt authorized: release after failed capture failed")
	}
}

// markAttemptCaptured settles an attempt whose payment the provider has
// captured. capturedAt is the provider's capture time (zero: now); it is what
// both the order's paid_at and the faktura marker record. payerName (nil:
// none) returns the name the provider shared, to name a guest order that has
// none yet. It is called only after the status update, so a slow or failing
// provider lookup cannot hold up marking the order paid.
func (s orderSettlement) markAttemptCaptured(ctx context.Context, a paymentAttempt, capturedAt time.Time, payerName func(context.Context) string) {
	if capturedAt.IsZero() {
		capturedAt = time.Now()
	}
	tag, err := s.db.Exec(ctx,
		`UPDATE payment_attempts SET status = 'captured', updated_at = now()
		  WHERE id = $1 AND status <> 'captured'`,
		a.ID,
	)
	if err != nil {
		s.log.Error().Err(err).Str("attempt_id", a.ID).Msg("attempt captured: attempt update failed")
	}
	firstCapture := err == nil && tag.RowsAffected() == 1

	if !s.payOrder(ctx, a, capturedAt) && firstCapture {
		s.logIfPaidTwice(ctx, a)
	}

	// Guest orders (QR / walk-up) show the payer instead of "Gjest" in the
	// harbor overview. Never overwrites a linked member or a name the payer
	// typed on the checkout page.
	if name := callPayerName(ctx, payerName); name != "" {
		if _, err := s.db.Exec(ctx,
			`UPDATE orders SET guest_name = $2
			  WHERE id = $1 AND user_id IS NULL AND COALESCE(guest_name, '') = ''`,
			a.OrderID, name,
		); err != nil {
			s.log.Error().Err(err).Str("order_id", a.OrderID).Msg("order paid: backfill guest name failed")
		}
	}

	s.markInvoicesPaidOnline(ctx, a, capturedAt)
}

// payOrder moves the order to paid from pending or failed and reports whether
// it did. A capture means the money was taken, so it pays an order an earlier
// terminal event failed; a paid or refunded order keeps its state, so a
// replayed capture cannot move paid_at.
func (s orderSettlement) payOrder(ctx context.Context, a paymentAttempt, capturedAt time.Time) bool {
	var from string
	err := s.db.QueryRow(ctx,
		`WITH prev AS (SELECT id, status FROM orders WHERE id = $1 FOR UPDATE)
		 UPDATE orders o
		    SET status = 'paid', paid_at = $2, provider = $3, updated_at = now()
		   FROM prev
		  WHERE o.id = prev.id AND prev.status IN ('pending', 'failed')
		 RETURNING prev.status::text`,
		a.OrderID, capturedAt, a.Provider,
	).Scan(&from)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			s.log.Error().Err(err).Str("order_id", a.OrderID).Msg("order paid: order update failed")
		}
		return false
	}
	if from == "failed" {
		s.log.Warn().Str("order_id", a.OrderID).Str("attempt_id", a.ID).
			Msg("order paid: capture arrived for an order marked failed")
	}
	return true
}

// logIfPaidTwice reports a capture on an order another attempt already paid:
// the payer was charged twice and one payment needs refunding.
func (s orderSettlement) logIfPaidTwice(ctx context.Context, a paymentAttempt) {
	var other bool
	if err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM payment_attempts
		                 WHERE order_id = $1 AND id <> $2 AND status = 'captured')`,
		a.OrderID, a.ID,
	).Scan(&other); err != nil {
		s.log.Error().Err(err).Str("order_id", a.OrderID).Msg("order paid: double-payment check failed")
		return
	}
	if other {
		s.log.Error().Str("order_id", a.OrderID).Str("attempt_id", a.ID).Str("provider", a.Provider).
			Msg("double payment: order already paid by another attempt, needs refund")
	}
}

// markInvoicesPaidOnline records, for every faktura the order settles, a soft
// "payment received" marker only — deliberately NOT payment_id, and no GL
// entry. The settlement import stays the single source of the paid status and
// the journal posting, and it matches only invoices with payment_id IS NULL;
// setting payment_id here would orphan that reconciliation. The marker drives
// the "mottatt, venter oppgjør" indicator until settlement clears it.
// API-primary GL ownership is BRY-256, gated on BRY-4. A Vipps capture also
// sets the legacy vipps_paid_at, so the column stays consistent for as long
// as it exists.
func (s orderSettlement) markInvoicesPaidOnline(ctx context.Context, a paymentAttempt, capturedAt time.Time) {
	if _, err := s.db.Exec(ctx,
		`UPDATE invoices
		    SET online_paid_at = $2, online_paid_provider = $3::text,
		        vipps_paid_at = CASE WHEN $3::text = $4::text THEN COALESCE(vipps_paid_at, $2) ELSE vipps_paid_at END
		  WHERE online_paid_at IS NULL
		    AND id IN (SELECT invoice_id FROM order_sources WHERE order_id = $1)`,
		a.OrderID, capturedAt, a.Provider, vipps.ProviderName,
	); err != nil {
		s.log.Error().Err(err).Str("order_id", a.OrderID).Msg("order paid: invoice marker failed")
	}
}

// backfillGuestEmail records the email a provider collected on a guest order
// that has none, so the club can reach a walk-up payer. Never overwrites a
// linked member or an email the payer typed.
func (s orderSettlement) backfillGuestEmail(ctx context.Context, a paymentAttempt, email string) {
	if email == "" {
		return
	}
	if _, err := s.db.Exec(ctx,
		`UPDATE orders SET guest_email = $2
		  WHERE id = $1 AND user_id IS NULL AND COALESCE(guest_email, '') = ''`,
		a.OrderID, email,
	); err != nil {
		s.log.Error().Err(err).Str("order_id", a.OrderID).Msg("order paid: backfill guest email failed")
	}
}

func callPayerName(ctx context.Context, payerName func(context.Context) string) string {
	if payerName == nil {
		return ""
	}
	return payerName(ctx)
}

// markAttemptFailed records an abandoned, expired or refused attempt. The
// order fails only when this is its current attempt and it is still pending:
// a payer who has since started a newer attempt is still paying. The current
// attempt is the newest one the provider created (it has a reference), so a
// row whose provider call never returned can't shadow a real payment. An
// authorized attempt that fails stops counting as authorized, so a later
// attempt on the order can be captured. 'failed' is the terminal-negative
// order_status (there is no 'cancelled'); the payer can retry with a fresh
// attempt on the same durable order.
func (s orderSettlement) markAttemptFailed(ctx context.Context, a paymentAttempt) {
	if _, err := s.db.Exec(ctx,
		`UPDATE payment_attempts SET status = 'failed', updated_at = now()
		  WHERE id = $1 AND status IN ('created', 'authorized')`,
		a.ID,
	); err != nil {
		s.log.Error().Err(err).Str("attempt_id", a.ID).Msg("attempt failed: attempt update failed")
	}
	if _, err := s.db.Exec(ctx,
		`UPDATE orders o SET status = 'failed', updated_at = now()
		  WHERE o.id = $1 AND o.status = 'pending'
		    AND $2::uuid = (SELECT pa.id FROM payment_attempts pa
		                     WHERE pa.order_id = o.id AND pa.provider_reference IS NOT NULL
		                     ORDER BY pa.created_at DESC, pa.id DESC
		                     LIMIT 1)`,
		a.OrderID, a.ID,
	); err != nil {
		s.log.Error().Err(err).Str("order_id", a.OrderID).Msg("order failed: order update failed")
	}
}
