// Package membership grants membership when a join payment settles (BRY-630)
// and tells the board about a refunded join payment or a withheld grant. It is
// called from inside the transactions that settle money: the online capture
// (handlers) and the bank back-link of a faktura (accounting), so it depends
// on neither.
//
// The rule: membership is granted only on real money for a membership_join
// purpose whose join line is intact, never on a voided or credited faktura,
// and granting is idempotent. A refund never revokes; it tells the board.
package membership

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/finance"
)

// Purpose is the orders.purpose / invoices.purpose of a join payment.
const Purpose = "membership_join"

// Grant sources, recorded on the membership.granted audit row.
const (
	// SourceOnline: a provider captured the join order, or an order paying
	// a join faktura.
	SourceOnline = "online"
	// SourceKID: a bank payment was linked to the join faktura, by a KID
	// match or a manual bank-row assignment.
	SourceKID = "kid"
	// SourceSweep: the hourly sweep found a paid join source whose grant had
	// not happened.
	SourceSweep = "sweep"
)

// Querier is what a grant needs of a transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Beginner opens a transaction: a pool, or a transaction (a savepoint).
type Beginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

var logger = zerolog.Nop()

// SetLogger sets where the settlement hooks log a grant that failed or was
// withheld. Called once at startup, before any settlement runs.
func SetLogger(l zerolog.Logger) {
	logger = l.With().Str("component", "membership").Logger()
}

// Outcome is what a grant attempt did for one join source (Ref, "order:<id>"
// or "invoice:<id>"). Withheld is set when the source carries real money for
// a join but failed the integrity check, or its payer cannot be granted:
// nothing was granted, and the board was sent a membership_grant_withheld
// notice.
type Outcome struct {
	Granted  bool
	ClubID   string
	UserID   string
	Ref      string
	Withheld string
}

// Withheld reasons.
const (
	withheldNoJoinLine     = "no line for a join price item"
	withheldLineNoAmount   = "join line has no amount"
	withheldLineMismatch   = "join line total differs from quantity times unit price"
	withheldTotalBelowLine = "total below the join line"
	withheldCaptureBelow   = "captured amount below the order total"
	withheldNoUser         = "join source has no user"
	withheldDeactivated    = "user is deactivated"
	withheldNotApplicant   = "user is neither applicant nor member"
)

// joinFacts are the amounts the integrity check compares, in øre so the
// comparison is exact.
type joinFacts struct {
	clubID, userID *string
	// lineTotalOre is the join line's stored total and lineCalcOre its
	// quantity × unit price; lineCalcOre is -1 when the source has no join
	// line.
	lineTotalOre int64
	lineCalcOre  int64
	totalOre     int64
}

// check applies the integrity rule shared by orders and fakturas, judging the
// source against itself rather than against today's price: it holds a join
// line whose stored total is its quantity × unit price and is positive, and it
// charges at least that line in total. A price changed, or joining closed,
// after the applicant confirmed does not withhold what they paid.
func (f joinFacts) check() string {
	switch {
	case f.userID == nil:
		return withheldNoUser
	case f.lineCalcOre < 0:
		return withheldNoJoinLine
	case f.lineTotalOre != f.lineCalcOre:
		return withheldLineMismatch
	case f.lineTotalOre <= 0:
		return withheldLineNoAmount
	case f.totalOre < f.lineTotalOre:
		return withheldTotalBelowLine
	}
	return ""
}

// joinLineSQL selects the source's join line as (stored total, quantity ×
// unit price) in øre. A join line is one whose price item is the club's, and
// is either the club's current join item or a membership item: a join source
// keeps its line when the club has since moved join_price_item_id to another
// item or unset it. The current join item wins when there are several.
// lines/fk/total name the line table, its source column and its total
// column; src is the source table alias.
func joinLineSQL(lines, fk, total, src string) string {
	return `(SELECT ARRAY[round(l.` + total + ` * 100)::bigint, round(l.quantity * l.unit_price * 100)::bigint]
	           FROM ` + lines + ` l
	           JOIN price_items p ON p.id = l.price_item_id AND p.club_id = ` + src + `.club_id
	           JOIN clubs c ON c.id = ` + src + `.club_id
	          WHERE l.` + fk + ` = ` + src + `.id
	            AND (p.id = c.join_price_item_id OR p.category = 'membership')
	          ORDER BY p.id IS NOT DISTINCT FROM c.join_price_item_id DESC, l.` + total + ` DESC
	          LIMIT 1)`
}

func (f *joinFacts) setLine(line []int64) {
	f.lineCalcOre = -1
	if len(line) == 2 {
		f.lineTotalOre, f.lineCalcOre = line[0], line[1]
	}
}

// GrantForOrderInSavepoint is GrantForOrderTx in a savepoint of tx, for the
// capture's settlement: a grant that fails is rolled back alone and logged,
// and the payment still settles; the hourly sweep grants it later. A grant
// that succeeds commits with the settlement.
func GrantForOrderInSavepoint(ctx context.Context, tx Beginner, orderID, source string) []Outcome {
	var out []Outcome
	err := inSavepoint(ctx, tx, func(sp pgx.Tx) error {
		var err error
		out, err = GrantForOrderTx(ctx, sp, orderID, source)
		return err
	})
	if err != nil {
		logger.Error().Err(err).Str("order_id", orderID).Str("source", source).
			Msg("join payment settled but the membership grant failed; the sweep retries it")
		return nil
	}
	logWithheld(out)
	return out
}

// GrantForInvoiceInSavepoint is GrantForInvoiceTx in a savepoint of tx, for
// the bank back-link, with the same failure handling as
// GrantForOrderInSavepoint.
func GrantForInvoiceInSavepoint(ctx context.Context, tx Beginner, invoiceID, source string) Outcome {
	var out Outcome
	err := inSavepoint(ctx, tx, func(sp pgx.Tx) error {
		var err error
		out, err = GrantForInvoiceTx(ctx, sp, invoiceID, source)
		return err
	})
	if err != nil {
		logger.Error().Err(err).Str("invoice_id", invoiceID).Str("source", source).
			Msg("join payment settled but the membership grant failed; the sweep retries it")
		return Outcome{}
	}
	logWithheld([]Outcome{out})
	return out
}

func logWithheld(out []Outcome) {
	for _, o := range out {
		if o.Withheld != "" {
			logger.Warn().Str("source", o.Ref).Str("user_id", o.UserID).Str("reason", o.Withheld).
				Msg("join payment settled but membership withheld; the board was told")
		}
	}
}

// GrantForOrderTx grants membership for a captured join order and for every
// join faktura the order pays (markInvoicesPaidOnline has marked those in the
// same transaction). An order that is no join and pays no join faktura is a
// no-op. The outcomes are those of the join sources found, in order.
func GrantForOrderTx(ctx context.Context, q Querier, orderID, source string) ([]Outcome, error) {
	var out []Outcome
	o, isJoin, err := grantOrder(ctx, q, orderID, source)
	if err != nil {
		return nil, err
	}
	if isJoin {
		out = append(out, o)
	}
	var invoiceIDs []string
	if err := q.QueryRow(ctx,
		`SELECT COALESCE(array_agg(i.id::text ORDER BY i.id), '{}')
		   FROM order_sources s JOIN invoices i ON i.id = s.invoice_id
		  WHERE s.order_id = $1 AND i.purpose = $2`,
		orderID, Purpose,
	).Scan(&invoiceIDs); err != nil {
		return nil, fmt.Errorf("load the order's join fakturas: %w", err)
	}
	for _, id := range invoiceIDs {
		o, _, err := grantInvoice(ctx, q, id, source)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

func grantOrder(ctx context.Context, q Querier, orderID, source string) (Outcome, bool, error) {
	var (
		f           joinFacts
		line        []int64
		status      string
		capturedOre int64
	)
	err := q.QueryRow(ctx,
		`SELECT o.club_id::text, o.user_id::text, o.status::text,
		        round(o.total_amount * 100)::bigint,
		        COALESCE((SELECT max(round(pa.amount * 100))::bigint FROM payment_attempts pa
		                   WHERE pa.order_id = o.id AND pa.status = 'captured'), 0),
		        `+joinLineSQL("order_lines", "order_id", "total_price", "o")+`
		   FROM orders o
		  WHERE o.id = $1 AND o.purpose = $2`,
		orderID, Purpose,
	).Scan(&f.clubID, &f.userID, &status, &f.totalOre, &capturedOre, &line)
	if errors.Is(err, pgx.ErrNoRows) {
		return Outcome{}, false, nil
	}
	if err != nil {
		return Outcome{}, false, fmt.Errorf("load join order: %w", err)
	}
	f.setLine(line)
	// Only a capture pays an order (payOrder); a refunded order is money
	// returned, not a grant.
	if status != "paid" {
		return Outcome{}, true, nil
	}
	o := Outcome{ClubID: deref(f.clubID), UserID: deref(f.userID), Ref: "order:" + orderID}
	switch reason := f.check(); {
	case reason != "":
		o.Withheld = reason
	case capturedOre < f.totalOre:
		o.Withheld = withheldCaptureBelow
	default:
		o, err = grantJoinMembershipTx(ctx, q, o, source, map[string]any{"order_id": orderID})
		if err != nil {
			return o, true, err
		}
	}
	return o, true, queueWithheldTx(ctx, q, o)
}

// GrantForInvoiceTx grants membership for a join faktura that real money has
// settled: a linked bank payment (payment_id) or an online capture
// (online_paid_at). A voided or credited faktura never grants, whatever
// settled it: that is why this does not use finance.SettledForMemberSQL. A
// faktura that is no join is a no-op.
func GrantForInvoiceTx(ctx context.Context, q Querier, invoiceID, source string) (Outcome, error) {
	o, _, err := grantInvoice(ctx, q, invoiceID, source)
	return o, err
}

func grantInvoice(ctx context.Context, q Querier, invoiceID, source string) (Outcome, bool, error) {
	var (
		f                      joinFacts
		line                   []int64
		paid, voided, credited bool
	)
	err := q.QueryRow(ctx,
		`SELECT i.club_id::text, i.user_id::text,
		        (i.payment_id IS NOT NULL OR i.online_paid_at IS NOT NULL),
		        i.status = 'voided', `+finance.CreditedSQL("i")+`,
		        round(i.total_amount * 100)::bigint,
		        `+joinLineSQL("invoice_lines", "invoice_id", "line_total", "i")+`
		   FROM invoices i
		  WHERE i.id = $1 AND i.purpose = $2`,
		invoiceID, Purpose,
	).Scan(&f.clubID, &f.userID, &paid, &voided, &credited, &f.totalOre, &line)
	if errors.Is(err, pgx.ErrNoRows) {
		return Outcome{}, false, nil
	}
	if err != nil {
		return Outcome{}, false, fmt.Errorf("load join faktura: %w", err)
	}
	f.setLine(line)
	if !paid || voided || credited {
		return Outcome{}, true, nil
	}
	o := Outcome{ClubID: deref(f.clubID), UserID: deref(f.userID), Ref: "invoice:" + invoiceID}
	if reason := f.check(); reason != "" {
		o.Withheld = reason
	} else {
		o, err = grantJoinMembershipTx(ctx, q, o, source, map[string]any{"invoice_id": invoiceID})
		if err != nil {
			return o, true, err
		}
	}
	return o, true, queueWithheldTx(ctx, q, o)
}

// queueWithheldTx tells the board, once per join source, that real money for a
// join was paid but membership was withheld: a membership_grant_withheld
// delivery and a membership.grant_withheld audit row. The unique ref index
// keeps a redelivery or the next sweep from telling it again. A source with
// no user has no one to name and is only logged.
func queueWithheldTx(ctx context.Context, q Querier, o Outcome) error {
	if o.Withheld == "" || o.UserID == "" {
		return nil
	}
	details := map[string]any{"ref": o.Ref, "reason": o.Withheld}
	payload, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("marshal withheld payload: %w", err)
	}
	var id string
	err = q.QueryRow(ctx,
		`INSERT INTO deliveries (kind, club_id, user_id, email, status, payload)
		 VALUES ($1, $2, $3, '', $4, $5)
		 ON CONFLICT (kind, (payload->>'ref')) WHERE `+noticeRefIndexPredicate+` DO NOTHING
		 RETURNING id`,
		broadcast.KindMembershipGrantWithheld, o.ClubID, o.UserID, broadcast.DeliveryPending, payload,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("queue withheld notice: %w", err)
	}
	return insertAudit(ctx, q, o.ClubID, audit.ActionMembershipGrantWithheld, o.UserID, details)
}

// noticeRefIndexPredicate is the predicate of the partial unique index
// idx_deliveries_membership_notice_ref (migration 000138); an ON CONFLICT
// that names it must repeat it.
const noticeRefIndexPredicate = `kind IN ('membership_join_refunded', 'membership_grant_withheld')`

func inSavepoint(ctx context.Context, b Beginner, fn func(pgx.Tx) error) error {
	sp, err := b.Begin(ctx)
	if err != nil {
		return err
	}
	defer sp.Rollback(ctx) //nolint:errcheck // no-op after commit
	if err := fn(sp); err != nil {
		return err
	}
	return sp.Commit(ctx)
}

// grantJoinMembershipTx makes an applicant a member: the member role is added
// and applicant removed, member_since is set unless it already is, and, only
// when this call added the member role, the membership.granted audit row and
// the welcome and board emails are written. It is idempotent: the unique
// (user, club, role) row lets one of two concurrent settlements add the role,
// and the other finds it and writes nothing more. The membership cancel fields
// are left alone: confirm cleared them when the applicant (re)joined, and a
// grant must not undo a cancellation made since.
func grantJoinMembershipTx(ctx context.Context, q Querier, out Outcome, source string, ref map[string]any) (Outcome, error) {
	clubID, userID := out.ClubID, out.UserID
	var active, applicant bool
	var addr string
	if err := q.QueryRow(ctx,
		`SELECT u.deactivated_at IS NULL, COALESCE(u.email, ''),
		        EXISTS (SELECT 1 FROM user_roles r
		                 WHERE r.user_id = u.id AND r.club_id = u.club_id AND r.role = 'applicant')
		   FROM users u WHERE u.id = $1 AND u.club_id = $2`,
		userID, clubID,
	).Scan(&active, &addr, &applicant); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			out.Withheld = withheldNoUser
			return out, nil
		}
		return out, fmt.Errorf("load applicant: %w", err)
	}
	if !active {
		out.Withheld = withheldDeactivated
		return out, nil
	}

	if !applicant {
		// Already a member (an earlier settlement or the board) and nothing
		// left to swap; a payer holding neither role is the board's call.
		var member bool
		if err := q.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM user_roles WHERE user_id = $1 AND club_id = $2 AND role = 'member')`,
			userID, clubID,
		).Scan(&member); err != nil {
			return out, fmt.Errorf("load member role: %w", err)
		}
		if !member {
			out.Withheld = withheldNotApplicant
		}
		return out, nil
	}

	tag, err := q.Exec(ctx,
		`INSERT INTO user_roles (user_id, club_id, role) VALUES ($1, $2, 'member')
		 ON CONFLICT (user_id, club_id, role) DO NOTHING`,
		userID, clubID,
	)
	if err != nil {
		return out, fmt.Errorf("grant member role: %w", err)
	}
	out.Granted = tag.RowsAffected() == 1
	if _, err := q.Exec(ctx,
		`DELETE FROM user_roles WHERE user_id = $1 AND club_id = $2 AND role = 'applicant'`,
		userID, clubID,
	); err != nil {
		return out, fmt.Errorf("remove applicant role: %w", err)
	}
	if _, err := q.Exec(ctx,
		`UPDATE users SET member_since = COALESCE(member_since, $3::date), updated_at = now()
		  WHERE id = $1 AND club_id = $2`,
		// Clubs have no timezone setting; the server's local zone is the
		// club's on every deployment today.
		userID, clubID, time.Now().In(time.Local).Format(time.DateOnly),
	); err != nil {
		return out, fmt.Errorf("set member_since: %w", err)
	}
	if !out.Granted {
		return out, nil
	}

	details := map[string]any{"source": source}
	for k, v := range ref {
		details[k] = v
	}
	if err := insertAudit(ctx, q, clubID, audit.ActionMembershipGranted, userID, details); err != nil {
		return out, err
	}
	payload, err := json.Marshal(details)
	if err != nil {
		return out, fmt.Errorf("marshal delivery payload: %w", err)
	}
	// The welcome row carries the address at grant time for the delivery
	// log; the board row's recipient is resolved when it is sent.
	for _, d := range []struct{ kind, to string }{
		{broadcast.KindMembershipWelcome, addr},
		{broadcast.KindMembershipBoardNotice, ""},
	} {
		if _, err := q.Exec(ctx,
			`INSERT INTO deliveries (kind, club_id, user_id, email, status, payload)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			d.kind, clubID, userID, d.to, broadcast.DeliveryPending, payload,
		); err != nil {
			return out, fmt.Errorf("queue %s: %w", d.kind, err)
		}
	}
	return out, nil
}

func insertAudit(ctx context.Context, q Querier, clubID, action, userID string, details map[string]any) error {
	b, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("marshal audit details: %w", err)
	}
	if _, err := q.Exec(ctx,
		`INSERT INTO audit_log (club_id, action, resource, resource_id, details)
		 VALUES ($1, $2, 'user', $3, $4)`,
		clubID, action, userID, b,
	); err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
