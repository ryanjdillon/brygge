package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/audit"
	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/roles"
)

// Cancelling membership online (BRY-633, decision D5): a member cancels from
// the portal, the cancellation takes effect at the end of the year it is made
// in (31 December), nothing paid is refunded, and the daily membership-end
// job deactivates the member once that date has passed. Until then the
// member may undo it. Dates are the server's local calendar: clubs have no
// timezone setting, and the server's zone is the club's on every deployment.

// maxCancelReasonRunes caps the member's optional reason.
const maxCancelReasonRunes = 2000

// codeMembershipEnded is the 409 code of an undo after the membership ended.
const codeMembershipEnded = "membership_ended"

// localToday is today's date in the server's zone, as YYYY-MM-DD.
func localToday(now time.Time) string {
	return now.In(time.Local).Format(time.DateOnly)
}

// cancelEndsOn is the date a cancellation made at now takes effect: 31
// December of now's year.
func cancelEndsOn(now time.Time) string {
	return time.Date(now.In(time.Local).Year(), time.December, 31, 0, 0, 0, 0, time.Local).Format(time.DateOnly)
}

// cancelRef names one cancellation, for the once-only key of its notices:
// the member and the moment they cancelled. A cancel, undo and cancel again
// is a new cancellation with its own notices.
func cancelRef(userID string, cancelledAt time.Time) string {
	return "cancel:" + userID + ":" + cancelledAt.UTC().Format(time.RFC3339Nano)
}

// cancelNoticeIndexPredicate is the predicate of the partial unique index
// idx_deliveries_membership_cancel_ref (migration 000139); an ON CONFLICT
// that names it must repeat it.
const cancelNoticeIndexPredicate = `kind IN ('membership_cancel_receipt', 'membership_cancel_board_notice',
                   'membership_cancel_undone', 'membership_end_refused')`

// queueCancelNoticeTx queues one cancellation email on the delivery queue,
// once per (kind, ref). to is the recipient address recorded for the
// delivery log, "" for a board notice (resolved when sent). It reports
// whether a row was queued.
func queueCancelNoticeTx(ctx context.Context, q dbtx, kind, clubID, userID, to string, payload map[string]any) (bool, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("marshal %s payload: %w", kind, err)
	}
	var id string
	err = q.QueryRow(ctx,
		`INSERT INTO deliveries (kind, club_id, user_id, email, status, payload)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (kind, (payload->>'ref')) WHERE `+cancelNoticeIndexPredicate+` DO NOTHING
		 RETURNING id`,
		kind, clubID, userID, to, bcast.DeliveryPending, b,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("queue %s: %w", kind, err)
	}
	return true, nil
}

// membershipAuditTx writes a membership.* audit row on the member.
func membershipAuditTx(ctx context.Context, q dbtx, clubID, actorID, action, userID string, details map[string]any) error {
	b, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("marshal audit details: %w", err)
	}
	if _, err := q.Exec(ctx,
		`INSERT INTO audit_log (club_id, actor_id, action, resource, resource_id, details)
		 VALUES ($1, $2, $3, 'user', $4, $5)`,
		clubID, actorID, action, userID, b,
	); err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}

// myMembership is GET /members/me/membership: the member's membership state
// and what the portal's "Meld deg ut" section must warn about.
type myMembership struct {
	MemberSince *string    `json:"member_since"`
	CancelledAt *time.Time `json:"cancelled_at"`
	EndsOn      *string    `json:"ends_on"`
	// CancelEndsOn is the date a cancellation made now would take effect.
	CancelEndsOn string `json:"cancel_ends_on"`
	// CanUndo is whether the cancellation may still be undone: the member
	// is still active, which holds through ends_on and past it while the
	// daily job may not deactivate them (the club's last admin).
	CanUndo          bool     `json:"can_undo"`
	OpenInvoices     int      `json:"open_invoices"`
	HoldsSlip        bool     `json:"holds_slip"`
	HarborShareOwner bool     `json:"harbor_share_owner"`
	OnWaitingList    bool     `json:"on_waiting_list"`
	BoardRoles       []string `json:"board_roles"`
}

func loadMyMembership(ctx context.Context, q dbtx, clubID, userID string, now time.Time) (myMembership, error) {
	m := myMembership{CancelEndsOn: cancelEndsOn(now)}
	err := q.QueryRow(ctx,
		`SELECT to_char(u.member_since, 'YYYY-MM-DD'), u.membership_cancelled_at,
		        to_char(u.membership_ends_on, 'YYYY-MM-DD'),
		        (u.membership_cancelled_at IS NOT NULL AND u.deactivated_at IS NULL),
		        u.harbor_share_owner,
		        (SELECT count(*) FROM invoices i
		          WHERE i.user_id = u.id AND i.club_id = u.club_id AND i.status = 'open'
		            AND (i.sent_at IS NOT NULL OR i.import_source IS NOT NULL)
		            AND `+finance.OwedByMemberSQL("i")+`),
		        EXISTS (SELECT 1 FROM slip_assignments sa
		                 WHERE sa.user_id = u.id AND sa.club_id = u.club_id
		                   AND sa.released_at IS NULL AND sa.assignment_type = 'permanent'),
		        EXISTS (SELECT 1 FROM waiting_list_entries w
		                 WHERE w.user_id = u.id AND w.club_id = u.club_id
		                   AND w.status IN ('active', 'offered')),
		        ARRAY(SELECT r.role::text FROM user_roles r
		               WHERE r.user_id = u.id AND r.club_id = u.club_id AND r.role::text = ANY($3)
		               ORDER BY r.role::text)
		   FROM users u
		  WHERE u.id = $1 AND u.club_id = $2 AND u.kind = 'person'`,
		userID, clubID, roles.Board,
	).Scan(&m.MemberSince, &m.CancelledAt, &m.EndsOn, &m.CanUndo, &m.HarborShareOwner,
		&m.OpenInvoices, &m.HoldsSlip, &m.OnWaitingList, &m.BoardRoles)
	if err != nil {
		return m, err
	}
	if m.BoardRoles == nil {
		m.BoardRoles = []string{}
	}
	return m, nil
}

// HandleGetMyMembership answers GET /members/me/membership.
func (h *MembersHandler) HandleGetMyMembership(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	m, err := loadMyMembership(ctx, h.db, claims.ClubID, claims.UserID, time.Now())
	if errors.Is(err, pgx.ErrNoRows) {
		Error(w, http.StatusNotFound, "member not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to load membership")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, m)
}

// HandleCancelMyMembership answers POST /members/me/membership/cancel
// {reason?}: the member cancels, effective 31 December of this year. The
// first call sets the cancellation, audits membership.cancelled and queues the
// member's receipt (the written notice) and the board's notice; a repeat
// changes nothing and answers the same state. Anyone with a session past the
// applicant gate may cancel, a board member who is not a member included.
func (h *MembersHandler) HandleCancelMyMembership(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	// AuthenticateSession already walls applicant-only sessions off this
	// route; this keeps the rule if the route is ever listed for them.
	if roles.IsApplicantOnly(claims.Roles) {
		Error(w, http.StatusForbidden, middleware.ErrMembershipNotCompleted)
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if utf8.RuneCountInString(reason) > maxCancelReasonRunes {
		Error(w, http.StatusBadRequest, fmt.Sprintf("reason must be at most %d characters", maxCancelReasonRunes))
		return
	}

	now := time.Now()
	queued, err := h.cancelMembership(ctx, claims.ClubID, claims.UserID, reason, now)
	if errors.Is(err, pgx.ErrNoRows) {
		Error(w, http.StatusNotFound, "member not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("failed to cancel membership")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if queued && h.deliveryKick != nil {
		h.deliveryKick()
	}
	h.respondMembership(w, ctx, claims.ClubID, claims.UserID, now)
}

func (h *MembersHandler) cancelMembership(ctx context.Context, clubID, userID, reason string, now time.Time) (bool, error) {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var cancelledAt *time.Time
	var addr string
	if err := tx.QueryRow(ctx,
		`SELECT membership_cancelled_at, COALESCE(email, '') FROM users
		  WHERE id = $1 AND club_id = $2 AND kind = 'person' AND deactivated_at IS NULL
		  FOR UPDATE`,
		userID, clubID,
	).Scan(&cancelledAt, &addr); err != nil {
		return false, err
	}
	if cancelledAt != nil {
		// Already cancelled: the first call's date and reason stand.
		return false, nil
	}

	endsOn := cancelEndsOn(now)
	var at time.Time
	if err := tx.QueryRow(ctx,
		`UPDATE users SET membership_cancelled_at = now(), membership_ends_on = $3::date,
		        membership_cancel_reason = NULLIF($4, ''), updated_at = now()
		  WHERE id = $1 AND club_id = $2
		 RETURNING membership_cancelled_at`,
		userID, clubID, endsOn, reason,
	).Scan(&at); err != nil {
		return false, fmt.Errorf("set cancellation: %w", err)
	}
	// The reason is the member's own text and stays out of the audit log,
	// which outlives an erasure; the row records only that one was given.
	if err := membershipAuditTx(ctx, tx, clubID, userID, audit.ActionMembershipCancelled, userID,
		map[string]any{"ends_on": endsOn, "reason_given": reason != ""}); err != nil {
		return false, err
	}
	// The receipt warns about the waiting list only to a member on it, as
	// things stood when they cancelled.
	var onWaitingList bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM waiting_list_entries
		                 WHERE user_id = $1 AND club_id = $2 AND status IN ('active', 'offered'))`,
		userID, clubID,
	).Scan(&onWaitingList); err != nil {
		return false, fmt.Errorf("read waiting list: %w", err)
	}
	payload := map[string]any{"ref": cancelRef(userID, at), "ends_on": endsOn}
	receipt := map[string]any{"ref": payload["ref"], "ends_on": endsOn, "on_waiting_list": onWaitingList}
	if _, err := queueCancelNoticeTx(ctx, tx, bcast.KindMembershipCancelReceipt, clubID, userID, addr, receipt); err != nil {
		return false, err
	}
	if _, err := queueCancelNoticeTx(ctx, tx, bcast.KindMembershipCancelBoardNotice, clubID, userID, "", payload); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// HandleUndoCancelMyMembership answers POST /members/me/membership/undo: the
// cancellation is cleared while the member is still active, audited
// membership.cancel_undone, and the board is told. That holds through ends_on,
// and past it for a member the daily job could not deactivate (the club's
// last admin). With no cancellation it changes nothing; once the membership
// has ended (the member is deactivated) it answers 409 membership_ended.
func (h *MembersHandler) HandleUndoCancelMyMembership(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	now := time.Now()
	queued, err := h.undoCancelMembership(ctx, claims.ClubID, claims.UserID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		Error(w, http.StatusNotFound, "member not found")
		return
	case errors.Is(err, errMembershipEnded):
		JSON(w, http.StatusConflict, map[string]string{
			"error": "the membership has ended and the cancellation can no longer be undone",
			"code":  codeMembershipEnded,
		})
		return
	case err != nil:
		h.log.Error().Err(err).Msg("failed to undo membership cancellation")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if queued && h.deliveryKick != nil {
		h.deliveryKick()
	}
	h.respondMembership(w, ctx, claims.ClubID, claims.UserID, now)
}

var errMembershipEnded = errors.New("membership ended")

func (h *MembersHandler) undoCancelMembership(ctx context.Context, clubID, userID string) (bool, error) {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var (
		cancelledAt *time.Time
		endsOn      *string
		ended       bool
	)
	// The daily job takes the same row lock before it deactivates, so an
	// undo and the end of the membership cannot both win.
	if err := tx.QueryRow(ctx,
		`SELECT membership_cancelled_at, to_char(membership_ends_on, 'YYYY-MM-DD'),
		        deactivated_at IS NOT NULL
		   FROM users
		  WHERE id = $1 AND club_id = $2 AND kind = 'person'
		  FOR UPDATE`,
		userID, clubID,
	).Scan(&cancelledAt, &endsOn, &ended); err != nil {
		return false, err
	}
	if cancelledAt == nil {
		return false, nil
	}
	if ended {
		return false, errMembershipEnded
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET membership_cancelled_at = NULL, membership_ends_on = NULL,
		        membership_cancel_reason = NULL, updated_at = now()
		  WHERE id = $1 AND club_id = $2`,
		userID, clubID,
	); err != nil {
		return false, fmt.Errorf("clear cancellation: %w", err)
	}
	details := map[string]any{}
	if endsOn != nil {
		details["ends_on"] = *endsOn
	}
	if err := membershipAuditTx(ctx, tx, clubID, userID, audit.ActionMembershipCancelUndone, userID, details); err != nil {
		return false, err
	}
	if _, err := queueCancelNoticeTx(ctx, tx, bcast.KindMembershipCancelUndone, clubID, userID, "",
		map[string]any{"ref": cancelRef(userID, *cancelledAt)}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (h *MembersHandler) respondMembership(w http.ResponseWriter, ctx context.Context, clubID, userID string, now time.Time) {
	m, err := loadMyMembership(ctx, h.db, clubID, userID, now)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to load membership")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, m)
}
