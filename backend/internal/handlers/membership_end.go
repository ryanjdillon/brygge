package handlers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brygge-klubb/brygge/internal/audit"
	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
)

// MembershipEndResult is what one run of EndCancelledMemberships did.
type MembershipEndResult struct {
	// Ended counts the members deactivated.
	Ended int
	// Withdrawn counts the waiting-list entries withdrawn from them.
	Withdrawn int
	// Refused lists the members not deactivated because each is their
	// club's only active admin; the board was told (once per member and end
	// date).
	Refused []string
	// Queued is whether any email was queued, so the caller kicks the
	// delivery worker.
	Queued bool
}

// EndCancelledMemberships ends every cancelled membership whose end date has
// passed (membership_ends_on before today, server-local) and whose member is
// still active: in one transaction per member it deactivates them
// (DeactivateMemberTx), withdraws their waiting-list entries, active and
// offered (WithdrawWaitingListTx), and writes membership.ended, all as the
// club's system actor. A member who is the club's only active admin is not
// deactivated: the transaction is rolled back, nothing is withdrawn, and the
// board gets a membership_end_refused notice instead, once. A deactivated
// member is not selected again, so the run is idempotent; the daily job loop
// runs it.
//
// systemActor resolves a club's system actor (accounting.Service.SystemActor).
// An error on one member is collected and the run continues with the next.
func EndCancelledMemberships(ctx context.Context, db *pgxpool.Pool, systemActor func(context.Context, string) (string, error), now time.Time) (MembershipEndResult, error) {
	var res MembershipEndResult
	rows, err := db.Query(ctx,
		`SELECT id::text, club_id::text, to_char(membership_ends_on, 'YYYY-MM-DD')
		   FROM users
		  WHERE kind = 'person' AND deactivated_at IS NULL
		    AND membership_ends_on < $1::date
		  ORDER BY club_id, membership_ends_on, id`,
		localToday(now),
	)
	if err != nil {
		return res, fmt.Errorf("list ended memberships: %w", err)
	}
	type due struct{ userID, clubID, endsOn string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
		var d due
		return d, r.Scan(&d.userID, &d.clubID, &d.endsOn)
	})
	if err != nil {
		return res, fmt.Errorf("list ended memberships: %w", err)
	}

	actors := map[string]string{}
	var errs []error
	for _, d := range list {
		actor, ok := actors[d.clubID]
		if !ok {
			actor, err = systemActor(ctx, d.clubID)
			if err != nil {
				errs = append(errs, fmt.Errorf("system actor for club %s: %w", d.clubID, err))
				continue
			}
			actors[d.clubID] = actor
		}
		withdrawn, err := endMembership(ctx, db, d.clubID, d.userID, d.endsOn, actor, localToday(now))
		switch {
		case errors.Is(err, errLastAdmin):
			res.Refused = append(res.Refused, d.userID)
			queued, err := queueCancelNoticeTx(ctx, db, bcast.KindMembershipEndRefused, d.clubID, d.userID, "",
				map[string]any{"ref": "end_refused:" + d.userID + ":" + d.endsOn, "ends_on": d.endsOn})
			if err != nil {
				errs = append(errs, fmt.Errorf("member %s: %w", d.userID, err))
			}
			res.Queued = res.Queued || queued
		case errors.Is(err, errNotDeactivatable), errors.Is(err, errMembershipNotDue):
			// Deactivated, undone or reactivated since the list was read;
			// nothing left to do.
		case err != nil:
			errs = append(errs, fmt.Errorf("member %s: %w", d.userID, err))
		default:
			res.Ended++
			res.Withdrawn += withdrawn
		}
	}
	return res, errors.Join(errs...)
}

// errMembershipNotDue means the member's cancellation is no longer passed or
// the member is no longer active: an undo, or a board deactivation (and
// perhaps a reactivation), landed between the list read and the member's
// transaction.
var errMembershipNotDue = errors.New("membership no longer due to end")

// endMembership deactivates one member whose cancellation has ended and
// withdraws their waiting-list entries, in one transaction, and reports how
// many entries it withdrew. today is the server-local date the run judges by.
func endMembership(ctx context.Context, db *pgxpool.Pool, clubID, userID, endsOn, actorID, today string) (int, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	// The club-admins lock comes before any row lock (DeactivateMemberTx
	// takes it again, re-entrantly). The list was read without locks, so the
	// cancellation is read again under the users row lock: an undo, or a
	// board deactivate and reactivate, since then must win.
	if err := lockClubAdmins(ctx, tx, clubID); err != nil {
		return 0, err
	}
	var due bool
	if err := tx.QueryRow(ctx,
		`SELECT membership_ends_on IS NOT NULL AND membership_ends_on < $3::date AND deactivated_at IS NULL
		   FROM users WHERE id = $1 AND club_id = $2
		   FOR UPDATE`,
		userID, clubID, today,
	).Scan(&due); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, errMembershipNotDue
		}
		return 0, fmt.Errorf("re-read cancellation: %w", err)
	}
	if !due {
		return 0, errMembershipNotDue
	}

	if err := DeactivateMemberTx(ctx, tx, clubID, userID, actorID); err != nil {
		return 0, err
	}
	withdrawn, err := WithdrawWaitingListTx(ctx, tx, clubID, userID, actorID)
	if err != nil {
		return 0, err
	}
	if err := membershipAuditTx(ctx, tx, clubID, actorID, audit.ActionMembershipEnded, userID,
		map[string]any{"ends_on": endsOn, "waiting_list_withdrawn": len(withdrawn)}); err != nil {
		return 0, err
	}
	return len(withdrawn), tx.Commit(ctx)
}
