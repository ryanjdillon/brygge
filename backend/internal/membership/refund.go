package membership

import (
	"context"
	"fmt"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/finance"
)

// joinRefundSourcesSQL lists the join payments money has gone back on, one row
// per join source keyed by ref: a succeeded provider refund of a join order,
// or of an order paying a join faktura, and a join faktura real money paid
// that is now credited (a credit note linked by hand, or the credit notes of
// a card refund). $1 is a club id, or empty for every club.
var joinRefundSourcesSQL = `
	SELECT DISTINCT ON (ref) ref, club_id, user_id FROM (
	    SELECT 'order:' || o.id AS ref, o.club_id, o.user_id
	      FROM payment_refunds r
	      JOIN payment_attempts pa ON pa.id = r.attempt_id
	      JOIN orders o ON o.id = pa.order_id
	     WHERE r.status = 'succeeded' AND o.purpose = '` + Purpose + `' AND o.user_id IS NOT NULL
	    UNION ALL
	    SELECT 'invoice:' || i.id, i.club_id, i.user_id
	      FROM payment_refunds r
	      JOIN payment_attempts pa ON pa.id = r.attempt_id
	      JOIN order_sources s ON s.order_id = pa.order_id
	      JOIN invoices i ON i.id = s.invoice_id
	     WHERE r.status = 'succeeded' AND i.purpose = '` + Purpose + `' AND i.user_id IS NOT NULL
	    UNION ALL
	    SELECT 'invoice:' || i.id, i.club_id, i.user_id
	      FROM invoices i
	     WHERE i.purpose = '` + Purpose + `' AND i.user_id IS NOT NULL
	       AND (i.payment_id IS NOT NULL OR i.online_paid_at IS NOT NULL)
	       AND ` + finance.CreditedSQL("i") + `
	) src
	WHERE $1 = '' OR club_id::text = $1
	ORDER BY ref`

// QueueJoinRefundNoticesTx queues the board's notice for every refunded or
// credited join payment that has none yet, with a membership.join_refunded
// audit row, and reports how many it queued. Membership is never revoked: the
// board decides. One notice per join source (the unique notice ref index), so a
// webhook redelivery, a second partial refund, a credit note after a card
// refund or the hourly sweep queues nothing more. An empty clubID covers every club.
func QueueJoinRefundNoticesTx(ctx context.Context, q Querier, clubID string) (int, error) {
	var n int
	err := q.QueryRow(ctx,
		`WITH src AS (`+joinRefundSourcesSQL+`),
		 queued AS (
		     INSERT INTO deliveries (kind, club_id, user_id, email, status, payload)
		     SELECT $2, club_id, user_id, '', $3, jsonb_build_object('ref', ref)
		       FROM src
		     ON CONFLICT (kind, (payload->>'ref')) WHERE `+noticeRefIndexPredicate+` DO NOTHING
		     RETURNING club_id, user_id, payload
		 ),
		 audited AS (
		     INSERT INTO audit_log (club_id, action, resource, resource_id, details)
		     SELECT club_id, $4, 'user', user_id::text, payload FROM queued
		     RETURNING 1
		 )
		 SELECT count(*) FROM audited`,
		clubID, broadcast.KindMembershipJoinRefunded, broadcast.DeliveryPending, audit.ActionMembershipJoinRefunded,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("queue join refund notices: %w", err)
	}
	return n, nil
}
