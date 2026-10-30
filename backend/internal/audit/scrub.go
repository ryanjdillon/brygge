package audit

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// MemberPIIKeys are the audit_log.details keys that hold a member's identity.
// New audit rows about a member store ids only; these are the keys older rows
// carry (the delete, deactivate, reactivate, create and update actions), and
// what erasure removes. Migration 000124 repeats the list for rows written
// before this existed.
var MemberPIIKeys = []string{
	"email", "full_name", "first_name", "last_name",
	"old_email", "old_full_name",
	"phone", "address_line", "postal_code", "city",
	"admin_notes",
}

// Execer is the write side shared by pgx.Tx and the pool.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// ScrubMemberPII removes MemberPIIKeys from the details of every audit row
// about the member: rows whose resource is the user, and rows whose details
// name them under user_id. The rows themselves stay (who did what, and when,
// is the point of the log); only the identity in them goes. It is the one
// sanctioned UPDATE of audit_log, called by every path that erases a member's
// identity, in the same transaction as the erasure. Idempotent. Returns the
// number of rows changed.
//
// audit_log is append-only by convention: no trigger, rule or privilege stops
// an UPDATE, so nothing else may rewrite it.
func ScrubMemberPII(ctx context.Context, db Execer, userID string) (int64, error) {
	tag, err := db.Exec(ctx,
		`UPDATE audit_log SET details = details - $2::text[]
		  WHERE jsonb_typeof(details) = 'object'
		    AND details ?| $2::text[]
		    AND ((resource = 'user' AND resource_id = $1::text)
		         OR details->>'user_id' = $1::text)`,
		userID, MemberPIIKeys,
	)
	if err != nil {
		return 0, fmt.Errorf("scrub audit log: %w", err)
	}
	return tag.RowsAffected(), nil
}
