package membership

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/broadcast"
)

// ExpiredReviewRef is the notice ref of an expired join the board must
// review, for the join source ref names ("invoice:<id>" or "order:<id>"). Its
// own prefix keeps it apart from that source's withheld-grant notice, so
// either can still be told once.
func ExpiredReviewRef(ref string) string { return "join_expired:" + ref }

// QueueJoinExpiredReviewTx tells the board, once per join source, that a join
// left unpaid past its expiry has part of the money in, so it was neither
// credited nor deleted (BRY-631): a membership_grant_withheld delivery naming
// reason, and a membership.join_expired_review audit row. The unique notice
// ref index makes a later run queue nothing more. It reports whether this
// call queued the notice.
func QueueJoinExpiredReviewTx(ctx context.Context, q Querier, clubID, userID, ref, reason string) (bool, error) {
	details := map[string]any{"ref": ExpiredReviewRef(ref), "reason": reason}
	payload, err := json.Marshal(details)
	if err != nil {
		return false, fmt.Errorf("marshal review payload: %w", err)
	}
	var id string
	err = q.QueryRow(ctx,
		`INSERT INTO deliveries (kind, club_id, user_id, email, status, payload)
		 VALUES ($1, $2, $3, '', $4, $5)
		 ON CONFLICT (kind, (payload->>'ref')) WHERE `+noticeRefIndexPredicate+` DO NOTHING
		 RETURNING id`,
		broadcast.KindMembershipGrantWithheld, clubID, userID, broadcast.DeliveryPending, payload,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("queue expired join review: %w", err)
	}
	return true, insertAudit(ctx, q, clubID, audit.ActionMembershipJoinExpiredReview, userID, details)
}
