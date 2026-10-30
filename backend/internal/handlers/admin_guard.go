package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// Typed refusals of the admin-lifecycle guards. The SPA matches on the codes.
const (
	codeLastAdmin     = "last_admin"
	codeAdminRequired = "admin_required"
)

var (
	// errLastAdmin means the change would leave the club with no active admin.
	errLastAdmin = errors.New("the club's only active admin")
	// errAdminRequired means a non-admin caller tried to act on an admin.
	errAdminRequired = errors.New("only an admin can change an admin")
)

// rowQuerier is the read side shared by pgx.Tx and the pool.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// lockClubAdmins serialises every change that can remove an active admin from
// a club: demoting, deactivating, deleting or erasing one. It is a
// transaction-scoped advisory lock per club, so two concurrent calls on two
// different admins cannot both see "the other one is still there". It must be
// the first lock a transaction takes: the callers then take the users row
// lock, and a transaction that held a row lock while waiting here could
// deadlock with one holding this lock and waiting for that row.
func lockClubAdmins(ctx context.Context, tx pgx.Tx, clubID string) error {
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended('club_admins:' || $1::text, 0))`, clubID); err != nil {
		return fmt.Errorf("lock club admins: %w", err)
	}
	return nil
}

// holdsAdminRole reports whether the user holds the admin role in the club,
// whether or not the account is active.
func holdsAdminRole(ctx context.Context, q rowQuerier, clubID, userID string) (bool, error) {
	var holds bool
	if err := q.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM user_roles
		                 WHERE user_id = $1 AND club_id = $2 AND role = 'admin')`,
		userID, clubID,
	).Scan(&holds); err != nil {
		return false, fmt.Errorf("read admin role: %w", err)
	}
	return holds, nil
}

// ensureNotLastActiveAdmin returns errLastAdmin when the user is an active
// admin (holds the role, not deactivated) and no other active admin exists.
// A user who is not an active admin cannot be the last one. Call it after
// lockClubAdmins, inside the transaction that makes the change.
func ensureNotLastActiveAdmin(ctx context.Context, tx pgx.Tx, clubID, userID string) error {
	var isActiveAdmin bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM user_roles ur JOIN users u ON u.id = ur.user_id
		                 WHERE ur.user_id = $1 AND ur.club_id = $2 AND ur.role = 'admin'
		                   AND u.deactivated_at IS NULL)`,
		userID, clubID,
	).Scan(&isActiveAdmin); err != nil {
		return fmt.Errorf("read active admin: %w", err)
	}
	if !isActiveAdmin {
		return nil
	}
	var others int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM user_roles ur JOIN users u ON u.id = ur.user_id
		  WHERE ur.club_id = $1 AND ur.role = 'admin' AND ur.user_id <> $2
		    AND u.club_id = $1 AND u.deactivated_at IS NULL`,
		clubID, userID,
	).Scan(&others); err != nil {
		return fmt.Errorf("count other admins: %w", err)
	}
	if others == 0 {
		return errLastAdmin
	}
	return nil
}

// checkAdminTarget applies the rules for an action on a member who may be an
// admin. A target who holds the admin role can only be changed by an admin
// (the board may deactivate members, not the people above them), and when the
// action takes the target out of the active admins (removesAdmin) they must not
// be the last one. Call it after lockClubAdmins. It returns errAdminRequired,
// errLastAdmin or a database error.
func checkAdminTarget(ctx context.Context, tx pgx.Tx, callerRoles []string, clubID, userID string, removesAdmin bool) error {
	if err := requireAdminForAdminTarget(ctx, tx, callerRoles, clubID, userID); err != nil {
		return err
	}
	if !removesAdmin {
		return nil
	}
	return ensureNotLastActiveAdmin(ctx, tx, clubID, userID)
}

// requireAdminForAdminTarget returns errAdminRequired when the target holds
// the admin role and the caller does not. It is the check for every write on
// a member that the board can reach (profile, email, slips): the email in
// particular is a login credential, so editing an admin's would be a takeover.
func requireAdminForAdminTarget(ctx context.Context, q rowQuerier, callerRoles []string, clubID, userID string) error {
	holds, err := holdsAdminRole(ctx, q, clubID, userID)
	if err != nil {
		return err
	}
	if holds && !hasRole(callerRoles, "admin") {
		return errAdminRequired
	}
	return nil
}

// writeAdminGuard answers a refusal from checkAdminTarget or
// ensureNotLastActiveAdmin and reports whether err was one; any other error is
// the caller's to log and answer as a 500.
func writeAdminGuard(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, errLastAdmin):
		writeLastAdmin(w)
	case errors.Is(err, errAdminRequired):
		writeAdminRequired(w)
	default:
		return false
	}
	return true
}

// writeLastAdmin answers the typed 409 for errLastAdmin.
func writeLastAdmin(w http.ResponseWriter) {
	JSON(w, http.StatusConflict, map[string]string{
		"error": "this is the club's only active admin; make another member an admin first",
		"code":  codeLastAdmin,
	})
}

// writeAdminRequired answers the typed 403 for a non-admin caller acting on an
// admin or granting the admin role.
func writeAdminRequired(w http.ResponseWriter) {
	JSON(w, http.StatusForbidden, map[string]string{
		"error": "only an admin can change an admin",
		"code":  codeAdminRequired,
	})
}
