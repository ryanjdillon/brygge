package membership

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// SweepResult is what one healing sweep did.
type SweepResult struct {
	Granted       int
	RefundNotices int
	// Withheld are the paid join sources the integrity check (or the payer's
	// state) kept from granting, as "<source id>: <reason>", for the log.
	Withheld []string
}

// Sweep heals what settlement left undone: it grants every applicant whose
// join order or join faktura real money has paid, and queues the board's
// notice for every refunded join payment that has none. Settlement grants in
// its own transaction, so this finds work only where that grant failed and was
// rolled back alone, after a bug, or after a manual data fix; a grant here is
// audited with source "sweep". A source still withheld is reported in the
// result, and its board notice is not queued twice. Each source is granted in
// its own transaction, so one failure does not hold up the rest.
func Sweep(ctx context.Context, db Beginner) (SweepResult, error) {
	var res SweepResult
	type candidate struct{ kind, id string }
	var cands []candidate
	err := inSavepoint(ctx, db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT 'order', o.id::text
			   FROM orders o
			   JOIN user_roles ur ON ur.user_id = o.user_id AND ur.club_id = o.club_id AND ur.role = 'applicant'
			  WHERE o.purpose = $1 AND o.status = 'paid'
			 UNION ALL
			 SELECT 'invoice', i.id::text
			   FROM invoices i
			   JOIN user_roles ur ON ur.user_id = i.user_id AND ur.club_id = i.club_id AND ur.role = 'applicant'
			  WHERE i.purpose = $1 AND (i.payment_id IS NOT NULL OR i.online_paid_at IS NOT NULL)
			    AND i.status <> 'voided'`,
			Purpose,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.kind, &c.id); err != nil {
				return err
			}
			cands = append(cands, c)
		}
		return rows.Err()
	})
	if err != nil {
		return res, fmt.Errorf("list paid join sources: %w", err)
	}

	var firstErr error
	for _, c := range cands {
		var o Outcome
		err := inSavepoint(ctx, db, func(tx pgx.Tx) error {
			var err error
			if c.kind == "order" {
				o, _, err = grantOrder(ctx, tx, c.id, SourceSweep)
			} else {
				o, _, err = grantInvoice(ctx, tx, c.id, SourceSweep)
			}
			return err
		})
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("grant %s %s: %w", c.kind, c.id, err)
			}
			continue
		}
		if o.Granted {
			res.Granted++
		}
		if o.Withheld != "" {
			res.Withheld = append(res.Withheld, c.kind+" "+c.id+": "+o.Withheld)
		}
	}

	n, err := QueueJoinRefundNotices(ctx, db, "")
	if err != nil && firstErr == nil {
		firstErr = err
	}
	res.RefundNotices = n
	return res, firstErr
}

// QueueJoinRefundNotices is QueueJoinRefundNoticesTx in a transaction of its
// own, for the code that records a refund or a credit note and has committed.
func QueueJoinRefundNotices(ctx context.Context, db Beginner, clubID string) (int, error) {
	var n int
	err := inSavepoint(ctx, db, func(tx pgx.Tx) error {
		var err error
		n, err = QueueJoinRefundNoticesTx(ctx, tx, clubID)
		return err
	})
	return n, err
}
