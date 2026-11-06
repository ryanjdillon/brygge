package handlers

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// errNotDeactivatable means DeactivateMemberTx found no active member to
// deactivate: no such user in the club, or one already deactivated.
var errNotDeactivatable = errors.New("member not found or already deactivated")

// DeactivateMemberTx deactivates a member in tx as actorID: deactivated_at and
// deactivated_by are set, a user.deactivated audit row is written, and the
// member's sessions and developer tokens are dropped. It is the reversible
// deactivation: the board's button (HandleDeactivateUser) and the year-end end
// of a cancelled membership (EndCancelledMemberships) both call it. Erasure
// and GDPR deletion deactivate through revokeMemberAccess instead, which also
// ends every other way to log in.
//
// It takes the club-admins lock itself, so it must run before the transaction
// takes any row lock; the lock is re-entrant, so a caller that already took it
// may call this. It returns errLastAdmin when the member is the club's only
// active admin, and errNotDeactivatable when there is no active member to
// deactivate. Who may deactivate whom (the admin-target rule, not oneself) is
// the caller's check, made before calling.
func DeactivateMemberTx(ctx context.Context, tx pgx.Tx, clubID, userID, actorID string) error {
	if err := lockClubAdmins(ctx, tx, clubID); err != nil {
		return err
	}
	if err := ensureNotLastActiveAdmin(ctx, tx, clubID, userID); err != nil {
		return err
	}

	tag, err := tx.Exec(ctx,
		`UPDATE users SET deactivated_at = now(), deactivated_by = $3, updated_at = now()
		  WHERE id = $1 AND club_id = $2 AND deactivated_at IS NULL`,
		userID, clubID, actorID,
	)
	if err != nil {
		return fmt.Errorf("deactivate user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errNotDeactivatable
	}

	// The row's resource_id names the member; the details stay empty so the
	// log holds no name or address that outlives an erasure.
	if _, err := tx.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
		 VALUES ($1, $2, 'user.deactivated', 'user', $3, '{}')`,
		clubID, actorID, userID,
	); err != nil {
		return fmt.Errorf("audit deactivation: %w", err)
	}

	// Drop their sessions so they're logged out immediately rather than at
	// the next natural expiry (ValidateSession also rejects deactivated
	// members, so this is belt-and-suspenders).
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("clear sessions: %w", err)
	}

	// Reactivating must not bring a developer token back to life, so the
	// member's tokens are revoked now. Passkeys and TOTP stay: this is the
	// reversible state.
	return revokeDeveloperTokens(ctx, tx, clubID, userID)
}

// withdrawnEntry is one waiting-list entry WithdrawWaitingListTx withdrew,
// with the status it had.
type withdrawnEntry struct {
	entry     waitingListEntry
	oldStatus string
}

// WithdrawWaitingListTx withdraws every active or offered waiting-list entry
// the user holds in the club, in tx, and writes a withdraw audit row for each
// as actorID. An offered entry is withdrawn too: the place offered goes back
// to the board. It returns the entries withdrawn, none when the user is on no
// list. The member's own Withdraw button (HandleWithdraw) and the year-end end
// of a cancelled membership both call it.
func WithdrawWaitingListTx(ctx context.Context, tx pgx.Tx, clubID, userID, actorID string) ([]withdrawnEntry, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, status FROM waiting_list_entries
		  WHERE user_id = $1 AND club_id = $2 AND status IN ('active', 'offered')
		  ORDER BY created_at, id
		  FOR UPDATE`,
		userID, clubID,
	)
	if err != nil {
		return nil, fmt.Errorf("load waiting-list entries: %w", err)
	}
	type found struct{ id, status string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (found, error) {
		var f found
		return f, r.Scan(&f.id, &f.status)
	})
	if err != nil {
		return nil, fmt.Errorf("load waiting-list entries: %w", err)
	}

	out := make([]withdrawnEntry, 0, len(list))
	for _, f := range list {
		var e waitingListEntry
		if err := tx.QueryRow(ctx,
			`UPDATE waiting_list_entries
			    SET status = 'withdrawn', updated_at = now()
			  WHERE id = $1 AND club_id = $2
			 RETURNING id, user_id, club_id, position, is_local, status, offer_deadline, created_at, updated_at`,
			f.id, clubID,
		).Scan(
			&e.ID, &e.UserID, &e.ClubID, &e.Position,
			&e.IsLocal, &e.Status, &e.OfferDeadline,
			&e.CreatedAt, &e.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("withdraw waiting-list entry: %w", err)
		}
		if err := logAuditTx(ctx, tx, clubID, actorID, "withdraw", "waiting_list_entry", f.id,
			map[string]string{"status": f.status},
			map[string]string{"status": "withdrawn"},
		); err != nil {
			return nil, err
		}
		out = append(out, withdrawnEntry{entry: e, oldStatus: f.status})
	}
	return out, nil
}
