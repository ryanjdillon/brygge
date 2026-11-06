package handlers

import (
	"net/http"
	"time"

	"github.com/brygge-klubb/brygge/internal/middleware"
)

// emailBounce is a member whose current address hard-bounced.
type emailBounce struct {
	Status string    `json:"status"` // rejected | suppressed | complaint
	At     time.Time `json:"at"`
	Detail string    `json:"detail"`
}

// emailBouncesSQL picks, per member, the latest outcome row sent to the
// member's current address and keeps it when it is hard. A row counts when
// it is the member's own or carries no user (a faktura to a typed address),
// as accounting.DeleteMemberMailLog matches them; the address must be the
// current one either way, so a corrected address clears the flag. Pending
// statuses (sending, submitted, unknown, queued, deferred, failed) never
// hide an earlier bounce: they say nothing about the address yet. An SES
// row's detail is its own permanent-bounce diagnostic; a Stalwart row's is
// the journal sweep's outcome_detail.
const emailBouncesSQL = `
	SELECT user_id, status, at, detail FROM (
	  SELECT DISTINCT ON (u.id)
	         u.id::text AS user_id, s.status,
	         COALESCE(s.last_event_at, s.created_at) AS at,
	         COALESCE(s.outcome_detail,
	                  (SELECT e.diagnostic FROM mail_delivery_events e
	                    WHERE e.send_id = s.id AND e.event_type = 'bounce'
	                      AND e.bounce_type = 'Permanent'
	                    ORDER BY e.event_at DESC, e.id DESC LIMIT 1),
	                  '') AS detail
	    FROM users u
	    JOIN email_sends s
	      ON s.club_id = u.club_id
	     AND (s.user_id = u.id OR s.user_id IS NULL)
	     AND lower(s.recipient) = lower(u.email)
	   WHERE u.club_id = $1 AND u.kind = 'person'
	     AND s.status IN ('accepted', 'rejected_temporary', 'rejected', 'suppressed', 'complaint')
	   ORDER BY u.id, s.created_at DESC, s.id DESC
	) latest
	WHERE status IN ('rejected', 'suppressed', 'complaint')`

// HandleListEmailBounces returns the members whose last delivery outcome to
// their current address was a hard bounce, a suppression or a spam
// complaint, keyed by user id, for the badge on the member list. One query
// for the whole club: the list never asks per row.
func (h *AdminUsersHandler) HandleListEmailBounces(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rows, err := h.db.Query(ctx, emailBouncesSQL, claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("load member email bounces")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	bounces := map[string]emailBounce{}
	for rows.Next() {
		var id string
		var b emailBounce
		if err := rows.Scan(&id, &b.Status, &b.At, &b.Detail); err != nil {
			h.log.Error().Err(err).Msg("scan member email bounce")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		b.At = b.At.UTC()
		bounces[id] = b
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("read member email bounces")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"bounces": bounces})
}
