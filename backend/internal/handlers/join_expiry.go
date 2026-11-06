package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/membership"
)

// Expiry of an unpaid self-service join (BRY-631, ACC-14). An applicant whose
// join has seen no activity for joinExpiryAge is ended one of three ways:
//   - nothing was billed (only the pending join order): the order is
//     expired and the applicant deactivated through revokeMemberAccess.
//     With no faktura they are eligible for PurgeExpiredMemberPII at once,
//     which erases them on its next run; the users row is never deleted;
//   - a join faktura was issued and nothing was paid: it is credited in
//     full by a credit note (ACC-1) and the applicant deactivated, so the
//     purge erases them after retention. No invoice row is ever deleted or
//     changed;
//   - part of the money is in (a bank row with the faktura's KID or
//     assigned to it, or an authorized or captured card payment): nothing
//     is credited or deactivated, and the board is told once to review it.
//
// A join that real money has settled is never touched: settling grants
// membership (BRY-630), and a withheld grant is already the board's.

// joinExpiryAge is how long an unpaid join waits: from the last activity on
// it (the join order's last change, which a re-confirm or a payment attempt
// moves, and the faktura's issue).
const joinExpiryAge = 60 * 24 * time.Hour

// joinExpiredNote is the credit note's reason, on each of its lines.
const joinExpiredNote = "Innmelding ikkje fullført"

// Outcomes of an expired join. deactivated and credited are recorded on the
// membership.join_expired audit row; review writes the board's notice and
// membership.join_expired_review instead.
const (
	joinExpiredDeactivated = "deactivated"
	joinExpiredCredited    = "credited"
	joinExpiredReview      = "review"
)

// joinPartialReason is the review notice's reason.
const joinPartialReason = "the join was unpaid after 60 days but part of the money is in; it was not credited automatically"

// JoinExpiryResult is what one expiry run did.
type JoinExpiryResult struct {
	Deactivated int
	Credited    int
	// Reviews counts the board notices this run queued; a join already
	// under review is not counted again.
	Reviews int
}

// joinLastActivitySQL is the last activity on the join of user alias u: the
// latest change of a pending join order, and the issue of a join faktura or
// a change of its pay order.
const joinLastActivitySQL = `GREATEST(
	(SELECT max(o.updated_at) FROM orders o
	  WHERE o.club_id = u.club_id AND o.user_id = u.id AND o.purpose = 'membership_join' AND o.status = 'pending'),
	(SELECT max(GREATEST(i.created_at,
	                     (SELECT max(o.updated_at) FROM order_sources s JOIN orders o ON o.id = s.order_id
	                       WHERE s.invoice_id = i.id)))
	   FROM invoices i
	  WHERE i.club_id = u.club_id AND i.user_id = u.id AND i.purpose = 'membership_join'))`

// joinExpiredSQL is true for an active person of alias u holding exactly the
// applicant role, with an unpaid join (a pending join order or an owed join
// faktura) whose last activity is older than $1 seconds.
var joinExpiredSQL = `u.kind = 'person' AND u.deactivated_at IS NULL
	AND EXISTS (SELECT 1 FROM user_roles r WHERE r.user_id = u.id AND r.club_id = u.club_id AND r.role = 'applicant')
	AND NOT EXISTS (SELECT 1 FROM user_roles r WHERE r.user_id = u.id AND r.club_id = u.club_id AND r.role <> 'applicant')
	AND (EXISTS (SELECT 1 FROM orders o
	              WHERE o.club_id = u.club_id AND o.user_id = u.id AND o.purpose = 'membership_join' AND o.status = 'pending')
	     OR EXISTS (SELECT 1 FROM invoices i
	                 WHERE i.club_id = u.club_id AND i.user_id = u.id AND i.purpose = 'membership_join'
	                   AND ` + finance.OwedByMemberSQL("i") + `))
	AND ` + joinLastActivitySQL + ` < now() - make_interval(secs => $1)`

// ExpireUnpaidJoins ends every join left unpaid past joinExpiryAge, in every
// club, each in its own transaction so one failure does not hold up the rest.
// It is idempotent: an ended join no longer matches (the applicant is
// deactivated), and one under review queues its notice only once. Run by the
// hourly sweep.
func (h *InvoiceHandler) ExpireUnpaidJoins(ctx context.Context) (JoinExpiryResult, error) {
	var res JoinExpiryResult
	rows, err := h.db.Query(ctx,
		`SELECT u.club_id::text, u.id::text FROM users u WHERE `+joinExpiredSQL+` ORDER BY u.club_id, u.id`,
		joinExpiryAge.Seconds())
	if err != nil {
		return res, fmt.Errorf("list expired joins: %w", err)
	}
	type candidate struct{ clubID, userID string }
	cands, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (candidate, error) {
		var c candidate
		return c, r.Scan(&c.clubID, &c.userID)
	})
	if err != nil {
		return res, fmt.Errorf("list expired joins: %w", err)
	}

	var firstErr error
	for _, c := range cands {
		outcome, err := h.expireJoin(ctx, c.clubID, c.userID)
		if err != nil {
			h.log.Error().Err(err).Str("user_id", c.userID).Msg("join expiry failed")
			if firstErr == nil {
				firstErr = fmt.Errorf("expire join of %s: %w", c.userID, err)
			}
			continue
		}
		switch outcome {
		case joinExpiredDeactivated:
			res.Deactivated++
		case joinExpiredCredited:
			res.Credited++
		case joinExpiredReview:
			res.Reviews++
		}
	}
	return res, firstErr
}

// joinTargets are the rows an expiry locks: the user's live join fakturas
// and the orders that pay them or are pending join orders, each sorted by id.
type joinTargets struct {
	invoiceIDs, orderIDs []string
}

func loadJoinTargets(ctx context.Context, tx pgx.Tx, clubID, userID string) (joinTargets, error) {
	var t joinTargets
	err := tx.QueryRow(ctx,
		`WITH inv AS (
		     SELECT i.id FROM invoices i
		      WHERE i.club_id = $1 AND i.user_id = $2 AND i.purpose = 'membership_join' AND i.status <> 'voided')
		 SELECT COALESCE((SELECT array_agg(id::text ORDER BY id) FROM inv), '{}'),
		        COALESCE((SELECT array_agg(id::text ORDER BY id) FROM (
		            SELECT o.id FROM orders o
		             WHERE o.club_id = $1 AND o.user_id = $2 AND o.purpose = 'membership_join' AND o.status = 'pending'
		            UNION
		            SELECT s.order_id FROM order_sources s WHERE s.invoice_id IN (SELECT id FROM inv)) ord), '{}')`,
		clubID, userID,
	).Scan(&t.invoiceIDs, &t.orderIDs)
	if err != nil {
		return t, fmt.Errorf("load join rows: %w", err)
	}
	return t, nil
}

// lockJoinRows takes the expiry's locks: the join fakturas by id, then the
// orders by id, then the user. ok is false when the user is gone, or when the
// join's rows changed between the read and the locks; the next run retries.
func lockJoinRows(ctx context.Context, tx pgx.Tx, clubID, userID string) (bool, error) {
	locked, err := loadJoinTargets(ctx, tx, clubID, userID)
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx,
		`SELECT id FROM invoices WHERE id = ANY($1::uuid[]) ORDER BY id FOR NO KEY UPDATE`, locked.invoiceIDs,
	); err != nil {
		return false, fmt.Errorf("lock join fakturas: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT id FROM orders WHERE id = ANY($1::uuid[]) ORDER BY id FOR UPDATE`, locked.orderIDs,
	); err != nil {
		return false, fmt.Errorf("lock join orders: %w", err)
	}
	var found bool
	err = tx.QueryRow(ctx, `SELECT true FROM users WHERE id = $1 AND club_id = $2 FOR UPDATE`, userID, clubID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock user: %w", err)
	}
	now, err := loadJoinTargets(ctx, tx, clubID, userID)
	if err != nil {
		return false, err
	}
	return slices.Equal(now.invoiceIDs, locked.invoiceIDs) && slices.Equal(now.orderIDs, locked.orderIDs), nil
}

// expireJoin ends one applicant's expired join. It takes its locks in the
// order the capture settlement does (settleCapture: fakturas by id, then the
// order, then the payer through the grant): the join fakturas, then their pay
// orders and the pending join orders by id, then the user. Everything is
// decided after the locks, from a fresh read: a capture that committed while
// this waited shows as settled, and a faktura or order that appeared since
// skips the join until the next run. outcome is empty when nothing was done.
func (h *InvoiceHandler) expireJoin(ctx context.Context, clubID, userID string) (string, error) {
	actor, _ := h.systemActor(ctx, clubID)
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	ok, err := lockJoinRows(ctx, tx, clubID, userID)
	if err != nil || !ok {
		return "", err
	}

	var expired, settled bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users u WHERE u.id = $2 AND u.club_id = $3 AND `+joinExpiredSQL+`),
		        EXISTS (SELECT 1 FROM orders o WHERE o.club_id = $3 AND o.user_id = $2
		                   AND o.purpose = 'membership_join' AND o.status IN ('paid', 'refunded'))
		     OR EXISTS (SELECT 1 FROM invoices i WHERE i.club_id = $3 AND i.user_id = $2
		                   AND i.purpose = 'membership_join' AND i.status <> 'voided'
		                   AND (i.payment_id IS NOT NULL OR i.online_paid_at IS NOT NULL))`,
		joinExpiryAge.Seconds(), userID, clubID,
	).Scan(&expired, &settled); err != nil {
		return "", fmt.Errorf("recheck expiry: %w", err)
	}
	// A join that real money settled is the grant's (or the board's, when it
	// was withheld), never the expiry's.
	if !expired || settled {
		return "", nil
	}

	var invoiceIDs, orderIDs []string
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE((SELECT array_agg(i.id::text ORDER BY i.invoice_number) FROM invoices i
		                   WHERE i.club_id = $1 AND i.user_id = $2 AND i.purpose = 'membership_join'
		                     AND `+finance.OwedByMemberSQL("i")+`), '{}'),
		        COALESCE((SELECT array_agg(o.id::text ORDER BY o.id) FROM orders o
		                   WHERE o.club_id = $1 AND o.user_id = $2 AND o.purpose = 'membership_join'
		                     AND o.status = 'pending'), '{}')`,
		clubID, userID,
	).Scan(&invoiceIDs, &orderIDs); err != nil {
		return "", fmt.Errorf("load unpaid join: %w", err)
	}

	partial, err := joinHasPartialMoney(ctx, tx, clubID, invoiceIDs, orderIDs)
	if err != nil {
		return "", err
	}
	if partial {
		return h.queueJoinReview(ctx, tx, clubID, userID, invoiceIDs, orderIDs)
	}

	if len(invoiceIDs) > 0 {
		credited, err := creditExpiredJoinTx(ctx, tx, clubID, userID, actor, invoiceIDs, orderIDs)
		if err != nil {
			return "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", fmt.Errorf("commit: %w", err)
		}
		h.deliverJoinCreditNotes(ctx, clubID, credited)
		return joinExpiredCredited, nil
	}

	if err := expireJoinOrdersTx(ctx, tx, orderIDs); err != nil {
		return "", err
	}
	if _, err := revokeMemberAccess(ctx, tx, clubID, userID, actor); err != nil {
		return "", err
	}
	if err := insertJoinExpiredAudit(ctx, tx, clubID, userID, map[string]any{
		"outcome": joinExpiredDeactivated, "order_ids": orderIDs,
	}); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return joinExpiredDeactivated, nil
}

// queueJoinReview tells the board once about a part-paid expired join and
// commits; outcome is empty when it was told before.
func (h *InvoiceHandler) queueJoinReview(ctx context.Context, tx pgx.Tx, clubID, userID string, invoiceIDs, orderIDs []string) (string, error) {
	ref := "order:" + firstOf(orderIDs)
	if len(invoiceIDs) > 0 {
		ref = "invoice:" + invoiceIDs[0]
	}
	queued, err := membership.QueueJoinExpiredReviewTx(ctx, tx, clubID, userID, ref, joinPartialReason)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	if !queued {
		return "", nil
	}
	return joinExpiredReview, nil
}

// firstOf is the first id, or "" for none.
func firstOf(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// joinHasPartialMoney reports whether any money has reached the unpaid join:
// an incoming bank row assigned to one of its fakturas or carrying one's KID
// (a payment too small to settle it is never linked), or a card payment
// authorized or captured on its pay orders. Such a join is the board's to
// review, not to credit or end.
func joinHasPartialMoney(ctx context.Context, tx pgx.Tx, clubID string, invoiceIDs, orderIDs []string) (bool, error) {
	var partial bool
	err := tx.QueryRow(ctx,
		`SELECT EXISTS (
		          SELECT 1 FROM bank_import_rows b
		            JOIN bank_imports bi ON bi.id = b.bank_import_id AND bi.club_id = $1
		           WHERE b.amount > 0
		             AND (b.invoice_id = ANY($2::uuid[])
		                  OR b.kid_number IN (SELECT i.kid_number FROM invoices i
		                                       WHERE i.id = ANY($2::uuid[]) AND i.kid_number <> '')))
		     OR EXISTS (
		          SELECT 1 FROM payment_attempts pa
		           WHERE pa.club_id = $1 AND pa.status IN ('authorized', 'captured')
		             AND (pa.order_id = ANY($3::uuid[])
		                  OR pa.order_id IN (SELECT s.order_id FROM order_sources s WHERE s.invoice_id = ANY($2::uuid[]))))`,
		clubID, invoiceIDs, orderIDs,
	).Scan(&partial)
	if err != nil {
		return false, fmt.Errorf("check partial join payment: %w", err)
	}
	return partial, nil
}

// creditedJoinInvoice is a join faktura the expiry credited, for the steps
// after the commit.
type creditedJoinInvoice struct {
	note   *accounting.CreditedInFull
	booked bool
}

// creditExpiredJoinTx credits each owed join faktura in full, expires any
// pending join order left, deactivates the applicant and audits it, on tx.
func creditExpiredJoinTx(ctx context.Context, tx pgx.Tx, clubID, userID, actor string,
	invoiceIDs, orderIDs []string) ([]creditedJoinInvoice, error) {
	today := time.Now()
	var (
		out     []creditedJoinInvoice
		numbers []int
	)
	for _, id := range invoiceIDs {
		var booked bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM journal_entries je
			                 WHERE je.club_id = $2 AND je.source = 'invoice_sync'
			                   AND je.source_id = $1::text AND je.status = 'posted')`,
			id, clubID,
		).Scan(&booked); err != nil {
			return nil, fmt.Errorf("read join faktura booking: %w", err)
		}
		cn, err := accounting.CreditInvoiceInFullTx(ctx, tx, clubID, id, joinExpiredNote, today)
		if err != nil {
			return nil, fmt.Errorf("credit join faktura %s: %w", id, err)
		}
		out = append(out, creditedJoinInvoice{note: cn, booked: booked})
		numbers = append(numbers, cn.Number)
	}
	if err := expireJoinOrdersTx(ctx, tx, orderIDs); err != nil {
		return nil, err
	}
	if _, err := revokeMemberAccess(ctx, tx, clubID, userID, actor); err != nil {
		return nil, err
	}
	return out, insertJoinExpiredAudit(ctx, tx, clubID, userID, map[string]any{
		"outcome": joinExpiredCredited, "invoice_ids": invoiceIDs, "credit_note_numbers": numbers, "order_ids": orderIDs,
	})
}

// deliverJoinCreditNotes books each credit note as the system actor when its
// faktura is booked, and emails it to the applicant, after the commit. Both
// are best effort: a credit note of a sent faktura is sent itself, so
// SyncInvoices books a missed one beside its faktura, and a failed email is
// logged as an error for the treasurer to resend from the faktura list. The
// refund documents' delivery queue does not fit: its email speaks of a card
// refund.
func (h *InvoiceHandler) deliverJoinCreditNotes(ctx context.Context, clubID string, credited []creditedJoinInvoice) {
	for _, c := range credited {
		if c.booked {
			h.bookAsSystem(ctx, clubID, c.note.ID)
		}
	}
	if h.email == nil {
		return
	}
	club, err := loadClubInvoiceFields(ctx, h.db, clubID)
	if err != nil {
		h.log.Error().Err(err).Str("club_id", clubID).Msg("join expiry: credit notes issued but NOT emailed (club unreadable); resend them from the faktura list")
		return
	}
	for _, c := range credited {
		if c.note.DeliverTo == "" {
			continue
		}
		pdf, number, total, err := h.ensureInvoicePDF(ctx, clubID, c.note.ID, club)
		if err == nil {
			mailCtx := h.invoiceMailContext(ctx, email.KindInvoice, clubID, c.note.ID, "", total)
			err = h.email.SendWithAttachment(mailCtx, c.note.DeliverTo,
				email.CreditNoteSubject(club.Name, number), email.CreditNoteBody(c.note.DisplayName, club.Name, number, total),
				fmt.Sprintf("kreditnota-%d.pdf", number), pdf)
		}
		if err != nil {
			h.log.Error().Err(err).Str("invoice_id", c.note.ID).Int("credit_note_number", c.note.Number).
				Msg("join expiry: credit note issued but NOT emailed; resend it from the faktura list")
		}
	}
}

// expireJoinOrdersTx makes the pending join orders unpayable: order_status
// has no cancelled value, so they are marked failed and their pay token is
// cleared, which takes the /betal link down. They keep their user and
// purpose, so a capture of a payment started before still lands on a
// membership_join order of a deactivated applicant: the grant withholds it
// and tells the board.
func expireJoinOrdersTx(ctx context.Context, tx pgx.Tx, orderIDs []string) error {
	if len(orderIDs) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET status = 'failed', token = NULL, updated_at = now()
		  WHERE id = ANY($1::uuid[]) AND status = 'pending'`,
		orderIDs,
	); err != nil {
		return fmt.Errorf("expire join orders: %w", err)
	}
	return nil
}

func insertJoinExpiredAudit(ctx context.Context, tx pgx.Tx, clubID, userID string, details map[string]any) error {
	b, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("marshal audit details: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, action, resource, resource_id, details)
		 VALUES ($1, $2, 'user', $3, $4)`,
		clubID, audit.ActionMembershipJoinExpired, userID, b,
	); err != nil {
		return fmt.Errorf("audit join expiry: %w", err)
	}
	return nil
}
