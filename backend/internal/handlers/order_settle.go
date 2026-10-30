package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/vipps"
)

// orderSettlement records a payment provider's verdict on one payment attempt
// and, through it, on the checkout order the attempt pays. It is the only code
// that marks an order paid or failed, and it is called only from a provider's
// signature-verified webhook (see the "Only the payment provider can mark an
// order paid" invariant). Every write is guarded so a redelivered or
// out-of-order event is a no-op. A write that fails is returned to the webhook,
// which answers 5xx so the provider redelivers the event; the guards make the
// redelivery finish what the first delivery left undone (see the invariant
// "A webhook never acknowledges an event whose lookup or settlement failed").
// Only descriptive data (guest name and email, card brand, the double-payment
// diagnostic) is best-effort: a failure there is logged and changes nothing
// about whether the order is paid.
//
// An order can carry several attempts (a payer who abandons one clicks again
// and gets a fresh one). The newest is the order's current attempt; an older
// one can still be completed by the payer, so its capture still pays the
// order.
type orderSettlement struct {
	db  *pgxpool.Pool
	log zerolog.Logger
}

// settleWriteTimeout bounds the writes that record a provider outcome which
// has already happened.
const settleWriteTimeout = 15 * time.Second

// settleContext returns the context for the writes that follow a provider call
// that has already taken effect (a capture, a void). It outlives the request:
// a provider that times out or a client that disconnects after the money moved
// must not roll back a healthy settlement and leave the redelivery to redo it.
func settleContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), settleWriteTimeout)
}

// dbtx is what a settlement write needs of a pool or a transaction, so the
// same statement runs on either.
type dbtx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
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

// scanAttempt reads one attempt row. A missing row is (zero, false, nil); any
// other error is returned, because a webhook that cannot tell "not our payment"
// from "the database is down" would acknowledge an event it never handled.
func scanAttempt(row pgx.Row, provider string) (paymentAttempt, bool, error) {
	a := paymentAttempt{Provider: provider}
	if err := row.Scan(&a.ID, &a.OrderID, &a.Status, &a.Amount, &a.Currency, &a.Guest); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return paymentAttempt{}, false, nil
		}
		return paymentAttempt{}, false, err
	}
	return a, true, nil
}

// attemptByReference resolves a provider's reference to its attempt in the
// club, and whether one was found. The error is a failed lookup, which the
// webhook answers with a 5xx so the provider delivers the event again.
func (s orderSettlement) attemptByReference(ctx context.Context, clubID, provider, reference string) (paymentAttempt, bool, error) {
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
// that is not a UUID matches nothing rather than failing the query. The error
// is a failed lookup, as for attemptByReference.
func (s orderSettlement) attemptByID(ctx context.Context, clubID, provider, id string) (paymentAttempt, bool, error) {
	if _, err := uuid.Parse(id); err != nil {
		return paymentAttempt{}, false, nil
	}
	return scanAttempt(s.db.QueryRow(ctx,
		`SELECT `+attemptColumns+`
		   FROM payment_attempts pa
		   JOIN orders o ON o.id = pa.order_id
		  WHERE pa.club_id = $1 AND pa.provider = $2 AND pa.id = $3`,
		clubID, provider, id,
	), provider)
}

// clubLookup is a query for a club id and its arguments.
type clubLookup struct {
	query string
	args  []any
}

// firstClub returns the club id of the first lookup that finds a row, "" when
// none does. A lookup that fails is an error rather than a miss: a webhook that
// fell through to the next lookup (ultimately the instance club) would store
// and dispatch its event under the wrong club, and the provider would never
// redeliver it.
func firstClub(ctx context.Context, db *pgxpool.Pool, lookups []clubLookup) (string, error) {
	for _, l := range lookups {
		var clubID string
		err := db.QueryRow(ctx, l.query, l.args...).Scan(&clubID)
		if err == nil {
			return clubID, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}
	return "", nil
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
//
// A decision that fails (database error) is returned and answers authSkip: the
// webhook answers 5xx and the provider delivers the event again. Capturing
// without the row lock could take a payment the order does not need (a
// duplicate for an order another attempt already paid), which can only be
// refunded afterwards; a payer's authorization that is left undecided just
// waits for the redelivery, and lapses at the provider if none comes.
func (s orderSettlement) authorizeAttempt(ctx context.Context, a paymentAttempt) (authDecision, error) {
	d, err := s.decideAuthorization(ctx, a)
	if err != nil {
		s.log.Error().Err(err).Str("attempt_id", a.ID).Msg("attempt authorized: decision failed, not capturing")
		return authSkip, err
	}
	return d, nil
}

// decideAuthorization runs entirely on its transaction: it holds the order's
// row lock from before it reads anything to the commit, and must not touch
// another pool connection or the provider. Every error it returns leaves the
// attempt as it was (the transaction rolls back) and the answer unusable.
func (s orderSettlement) decideAuthorization(ctx context.Context, a paymentAttempt) (authDecision, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return authSkip, err
	}
	defer tx.Rollback(ctx)

	var orderStatus, own string
	var sibling bool
	if err := tx.QueryRow(ctx,
		`SELECT status::text FROM orders WHERE id = $1 FOR UPDATE`, a.OrderID,
	).Scan(&orderStatus); err != nil {
		return authSkip, err
	}
	if err := tx.QueryRow(ctx,
		`SELECT pa.status,
		        EXISTS (SELECT 1 FROM payment_attempts o
		                 WHERE o.order_id = pa.order_id AND o.id <> pa.id
		                   AND o.status IN ('authorized', 'captured'))
		   FROM payment_attempts pa WHERE pa.id = $1`, a.ID,
	).Scan(&own, &sibling); err != nil {
		return authSkip, err
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
	// updated_at is kept when the status does not change: a redelivery that
	// decides capture again for an attempt already authorized must not make it
	// look newly authorized, or the age of a stuck authorization (the
	// brygge.payment.attempt.oldest_authorized_age gauge) would reset on every
	// provider retry.
	if _, err := tx.Exec(ctx,
		`UPDATE payment_attempts
		    SET status = $2, updated_at = CASE WHEN status = $2 THEN updated_at ELSE now() END
		  WHERE id = $1 AND status IN ('created', 'authorized', 'failed')`,
		a.ID, next,
	); err != nil {
		return authSkip, err
	}
	if err := tx.Commit(ctx); err != nil {
		return authSkip, err
	}
	return d, nil
}

// releaseAuthorization returns an attempt whose capture failed to created. An
// attempt left authorized would make every later attempt on the order be
// voided, so a payer who retries could never pay; the uncaptured reservation
// lapses at the provider. A failed write is returned, so the webhook answers
// 5xx and the redelivery decides and releases again.
func (s orderSettlement) releaseAuthorization(ctx context.Context, a paymentAttempt) error {
	if _, err := s.db.Exec(ctx,
		`UPDATE payment_attempts SET status = 'created', updated_at = now()
		  WHERE id = $1 AND status = 'authorized'`,
		a.ID,
	); err != nil {
		s.log.Error().Err(err).Str("attempt_id", a.ID).Msg("attempt authorized: release after failed capture failed")
		return err
	}
	return nil
}

// markAttemptCaptured settles an attempt whose payment the provider has
// captured. capturedAt is the provider's capture time (zero: now); it is what
// both the order's paid_at and the faktura marker record. payerName (nil:
// none) returns the name the provider shared, to name a guest order that has
// none yet. The attempt, the order and the faktura markers are written in one
// transaction, so a failure leaves all of them as they were and the
// redelivered event settles them together: a half-settled capture (attempt
// captured, order still pending) would be read as done by the next
// authorization, which skips an attempt already captured. The payer name is
// looked up only after that transaction commits, so a slow or failing
// provider lookup cannot hold up marking the order paid, and a failed
// backfill is logged, not returned.
func (s orderSettlement) markAttemptCaptured(ctx context.Context, a paymentAttempt, capturedAt time.Time, payerName func(context.Context) string) error {
	if capturedAt.IsZero() {
		capturedAt = time.Now()
	}
	orderPaid, firstCapture, err := s.settleCapture(ctx, a, capturedAt)
	if err != nil {
		s.log.Error().Err(err).Str("attempt_id", a.ID).Str("order_id", a.OrderID).Msg("attempt captured: settlement failed")
		return err
	}
	if !orderPaid && firstCapture {
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
	return nil
}

// settleCapture is markAttemptCaptured's transaction. It reports whether this
// call moved the order to paid and whether it was the first to mark the
// attempt captured.
//
// Locks are taken fakturas (in id order), then the order (payOrder), then the
// attempt. The Stripe refund booking locks an order's fakturas first and writes
// the order after, so taking the order first here would deadlock with it; the
// decision and the refund request take order then attempt and never a faktura,
// which this order is consistent with. The decision's own transaction
// (authorizeAttempt) has committed before the provider is called and so holds
// nothing by now.
func (s orderSettlement) settleCapture(ctx context.Context, a paymentAttempt, capturedAt time.Time) (orderPaid, firstCapture bool, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx,
		`SELECT id FROM invoices
		  WHERE id IN (SELECT invoice_id FROM order_sources WHERE order_id = $1)
		  ORDER BY id FOR NO KEY UPDATE`, a.OrderID,
	); err != nil {
		return false, false, err
	}
	if orderPaid, err = s.payOrder(ctx, tx, a, capturedAt); err != nil {
		return false, false, err
	}
	tag, err := tx.Exec(ctx,
		`UPDATE payment_attempts SET status = 'captured', updated_at = now()
		  WHERE id = $1 AND status <> 'captured'`,
		a.ID,
	)
	if err != nil {
		return false, false, err
	}
	if err := s.markInvoicesPaidOnline(ctx, tx, a, capturedAt); err != nil {
		return false, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, false, err
	}
	return orderPaid, tag.RowsAffected() == 1, nil
}

// payOrder moves the order to paid from pending or failed and reports whether
// it did. A capture means the money was taken, so it pays an order an earlier
// terminal event failed; a paid or refunded order keeps its state, so a
// replayed capture cannot move paid_at (no row is not an error). It locks the
// order row whether or not it updates it.
func (s orderSettlement) payOrder(ctx context.Context, db dbtx, a paymentAttempt, capturedAt time.Time) (bool, error) {
	var from string
	err := db.QueryRow(ctx,
		`WITH prev AS (SELECT id, status FROM orders WHERE id = $1 FOR UPDATE)
		 UPDATE orders o
		    SET status = 'paid', paid_at = $2, provider = $3, updated_at = now()
		   FROM prev
		  WHERE o.id = prev.id AND prev.status IN ('pending', 'failed')
		 RETURNING prev.status::text`,
		a.OrderID, capturedAt, a.Provider,
	).Scan(&from)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if from == "failed" {
		s.log.Warn().Str("order_id", a.OrderID).Str("attempt_id", a.ID).
			Msg("order paid: capture arrived for an order marked failed")
	}
	return true, nil
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
//
// A replayed capture never marks a faktura a refund of this order has
// touched: not on an order the refund moved to refunded, and not on a
// faktura with a refund_documents row from this order (a refund that chose
// to collect again cleared the marker on purpose, and the faktura must read
// as open). A capture on the faktura's new order, after it was collected
// again, still marks it.
func (s orderSettlement) markInvoicesPaidOnline(ctx context.Context, db dbtx, a paymentAttempt, capturedAt time.Time) error {
	_, err := db.Exec(ctx,
		`UPDATE invoices
		    SET online_paid_at = $2, online_paid_provider = $3::text,
		        vipps_paid_at = CASE WHEN $3::text = $4::text THEN COALESCE(vipps_paid_at, $2) ELSE vipps_paid_at END
		  WHERE online_paid_at IS NULL
		    AND id IN (SELECT invoice_id FROM order_sources WHERE order_id = $1)
		    AND NOT EXISTS (SELECT 1 FROM orders o WHERE o.id = $1 AND o.status = 'refunded')
		    AND NOT EXISTS (SELECT 1 FROM refund_documents rd
		                     WHERE rd.invoice_id = invoices.id AND (rd.order_id = $1 OR rd.detached_order_id = $1))`,
		a.OrderID, capturedAt, a.Provider, vipps.ProviderName,
	)
	return err
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

// recordAttemptCard records the card brand and wallet a captured attempt was
// paid with, for the harbor overview. It runs after settlement and only
// describes the payment: a failure is logged and changes nothing about
// whether the order is paid. The first recorded value is kept, so a later
// source cannot overwrite it.
func (s orderSettlement) recordAttemptCard(ctx context.Context, attemptID, brand, wallet string) {
	if brand == "" {
		return
	}
	if _, err := s.db.Exec(ctx,
		`UPDATE payment_attempts SET card_brand = $2, card_wallet = NULLIF($3, '')
		  WHERE id = $1 AND card_brand IS NULL`,
		attemptID, brand, wallet,
	); err != nil {
		s.log.Warn().Err(err).Str("attempt_id", attemptID).Msg("attempt captured: recording card details failed")
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
// attempt on the same durable order. The two writes are separate and each is
// guarded, so when the second fails the redelivery repeats the first as a
// no-op and finishes the second.
func (s orderSettlement) markAttemptFailed(ctx context.Context, a paymentAttempt) error {
	if _, err := s.db.Exec(ctx,
		`UPDATE payment_attempts SET status = 'failed', updated_at = now()
		  WHERE id = $1 AND status IN ('created', 'authorized')`,
		a.ID,
	); err != nil {
		s.log.Error().Err(err).Str("attempt_id", a.ID).Msg("attempt failed: attempt update failed")
		return err
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
		return err
	}
	return nil
}
