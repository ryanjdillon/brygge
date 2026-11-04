package handlers

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// The delivery log's table path (BRY-590): every faktura mail since the send
// log landed has an email_sends row (related_type 'invoice'), and with SES as
// the relay, its outcome comes from the SES delivery events the worker stores
// in mail_delivery_events. The journal path in invoices_delivery_log.go and
// mail_journal.go only covers sends from before the first row, and sends that
// went through Stalwart.

// Statuses the table path adds to the delivery log, beside the journal
// path's. They are email_sends.status values (migration 000130).
const (
	deliverySending           = "sending"            // the row is written, the relay has not answered yet
	deliverySubmitted         = "submitted"          // the relay took the message, no event yet
	deliveryFailed            = "failed"             // the relay refused it before taking it; nothing was sent
	deliveryUnknown           = "unknown"            // the connection broke mid-send; the relay may have it
	deliveryRejectedTemporary = "rejected_temporary" // SES gave up after transient bounces
	deliverySuppressed        = "suppressed"         // SES did not send: the address is on the suppression list
	deliveryComplaint         = "complaint"          // the recipient marked the mail as spam
)

// suppressionRemover takes an address off the SES account-level suppression
// list; removed is false when it was not on it.
type suppressionRemover interface {
	Remove(ctx context.Context, address string) (removed bool, err error)
}

// SetSuppressionList enables the "remove from suppression list" action. Nil
// (no SES configured) makes the action answer 503.
func (h *InvoiceHandler) SetSuppressionList(s suppressionRemover) { h.suppression = s }

// tableSend is one email_sends row of a faktura.
type tableSend struct {
	id, kind, recipient, provider, status string
	errMsg                                *string
	createdAt                             time.Time
	hardBounced                           bool
}

// canUnsuppress reports whether a send was stopped by, or put its address on,
// the suppression list: SES refused it as suppressed, or it hard-bounced (SES
// adds the address on a hard bounce). A complaint ranks above both, so a
// send the recipient reported as spam never offers the action.
func canUnsuppress(provider, status string, hardBounced bool) bool {
	if provider != string(email.ProviderSES) {
		return false
	}
	return status == deliverySuppressed || (status == deliveryRejected && hardBounced)
}

// loadTableSends returns a faktura's email_sends rows, oldest first, with
// their SES events.
func (h *InvoiceHandler) loadTableSends(ctx context.Context, clubID, invoiceID string) ([]tableSend, map[string][]deliveryLogEvent, error) {
	rows, err := h.db.Query(ctx,
		`SELECT s.id::text, s.kind, s.recipient, s.provider, s.status, s.error, s.created_at,
		        EXISTS (SELECT 1 FROM mail_delivery_events e
		                 WHERE e.send_id = s.id AND e.event_type = 'bounce'
		                   AND e.bounce_type = 'Permanent')
		   FROM email_sends s
		  WHERE s.club_id = $1 AND s.related_type = 'invoice' AND s.related_id = $2
		  ORDER BY s.created_at, s.id`,
		clubID, invoiceID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var sends []tableSend
	var ids []string
	for rows.Next() {
		var s tableSend
		if err := rows.Scan(&s.id, &s.kind, &s.recipient, &s.provider, &s.status, &s.errMsg, &s.createdAt, &s.hardBounced); err != nil {
			return nil, nil, err
		}
		s.createdAt = s.createdAt.UTC()
		sends = append(sends, s)
		ids = append(ids, s.id)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	events := map[string][]deliveryLogEvent{}
	if len(ids) == 0 {
		return sends, events, nil
	}
	evRows, err := h.db.Query(ctx,
		`SELECT send_id::text, event_type, event_at,
		        COALESCE(bounce_type, ''), COALESCE(bounce_subtype, ''), COALESCE(delay_type, ''),
		        COALESCE(smtp_status, ''), COALESCE(diagnostic, ''), COALESCE(remote_mta, '')
		   FROM mail_delivery_events
		  WHERE send_id = ANY($1::uuid[])
		  ORDER BY event_at, id`,
		ids)
	if err != nil {
		return nil, nil, err
	}
	defer evRows.Close()
	for evRows.Next() {
		var sendID, typ, bounceType, bounceSub, delayType, smtpStatus, diag, mta string
		var at time.Time
		if err := evRows.Scan(&sendID, &typ, &at, &bounceType, &bounceSub, &delayType, &smtpStatus, &diag, &mta); err != nil {
			return nil, nil, err
		}
		ev := deliveryLogEvent{
			Timestamp: at.UTC(),
			Type:      typ,
			Message:   sesEventMessages[typ],
			Hostname:  mta,
			SMTPCode:  smtpReplyCode(diag),
			Details:   diag,
		}
		switch {
		case bounceType != "":
			ev.Subtype = joinNonEmpty(bounceType, bounceSub)
		case delayType != "":
			ev.Subtype = delayType
		}
		if ev.Details == "" {
			ev.Details = smtpStatus
		}
		events[sendID] = append(events[sendID], ev)
	}
	return sends, events, evRows.Err()
}

// sesEventMessages describe the event types in English for clients that do
// not translate the type.
var sesEventMessages = map[string]string{
	"send":              "Accepted by Amazon SES",
	"delivery":          "Delivered to the recipient's mail server",
	"delivery_delay":    "Delivery delayed",
	"bounce":            "Bounced",
	"complaint":         "Marked as spam by the recipient",
	"reject":            "Rejected by Amazon SES",
	"rendering_failure": "Rendering failure",
}

// smtpReplyRe finds the basic SMTP reply code in a diagnostic such as
// "smtp; 550 5.1.1 user unknown".
var smtpReplyRe = regexp.MustCompile(`\b[245]\d\d\b`)

func smtpReplyCode(diag string) int {
	n, _ := strconv.Atoi(smtpReplyRe.FindString(diag))
	return n
}

func joinNonEmpty(a, b string) string {
	if a == "" || b == "" {
		return a + b
	}
	return a + "/" + b
}

// deliveryKind labels a row like the journal path labels its sends.
// firstSend is whether no earlier faktura mail (not a reminder) exists.
func deliveryKind(kind string, firstSend bool) string {
	switch {
	case kind == string(email.KindInvoiceReminder):
		return "reminder"
	case firstSend:
		return "sent"
	default:
		return "resent"
	}
}

// fromTableSend builds the log entry for one row. An SES row carries its own
// outcome; a Stalwart row still needs the journal unless the sender already
// knows it failed, so needsJournal is set and the caller matches it.
func fromTableSend(s tableSend, events []deliveryLogEvent, kind string) (out deliveryLogSend, needsJournal bool) {
	out = deliveryLogSend{
		ID:        s.id,
		Kind:      kind,
		At:        s.createdAt,
		Status:    s.status,
		Provider:  s.provider,
		Recipient: s.recipient,
		Events:    events,
	}
	if out.Events == nil {
		out.Events = []deliveryLogEvent{}
	}
	if s.errMsg != nil {
		out.Details = *s.errMsg
	}
	if s.provider != string(email.ProviderSES) {
		return out, s.status != deliveryFailed
	}
	// The outcome is the latest event that says something about delivery;
	// a Send event only means SES took the message.
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == "send" {
			continue
		}
		e := events[i]
		at := e.Timestamp
		out.OutcomeAt = &at
		out.SMTPCode = e.SMTPCode
		out.RemoteHost = e.Hostname
		out.Details = e.Details
		break
	}
	out.CanUnsuppress = canUnsuppress(s.provider, s.status, s.hardBounced)
	return out, false
}

// HandleGetDeliveryStatuses returns the latest send status of every faktura
// that has an email_sends row, for the badge on the faktura list. One query
// for the whole club: the list never asks per row. Fakturas mailed only
// before the send log existed have no entry (their outcome needs the
// per-recipient journal read the delivery log does).
func (h *InvoiceHandler) HandleGetDeliveryStatuses(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rows, err := h.db.Query(ctx,
		`SELECT DISTINCT ON (related_id) related_id::text, status, created_at
		   FROM email_sends
		  WHERE club_id = $1 AND related_type = 'invoice' AND related_id IS NOT NULL
		  ORDER BY related_id, created_at DESC, id DESC`,
		claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("load invoice delivery statuses")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	type entry struct {
		Status string    `json:"status"`
		At     time.Time `json:"at"`
	}
	statuses := map[string]entry{}
	for rows.Next() {
		var id string
		var e entry
		if err := rows.Scan(&id, &e.Status, &e.At); err != nil {
			h.log.Error().Err(err).Msg("scan invoice delivery status")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		e.At = e.At.UTC()
		statuses[id] = e
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("read invoice delivery statuses")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"statuses": statuses})
}

// HandleUnsuppressRecipient takes the recipient of one faktura send off the
// SES account-level suppression list, so a resend can reach it. The send id
// names the address: the client never posts one, and only a send of this
// faktura in the caller's club that SES suppressed or that hard-bounced
// qualifies.
// https://docs.aws.amazon.com/ses/latest/APIReference-V2/API_DeleteSuppressedDestination.html
func (h *InvoiceHandler) HandleUnsuppressRecipient(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	invoiceID := chi.URLParam(r, "invoiceID")
	sendID := chi.URLParam(r, "sendID")
	if _, err := uuid.Parse(invoiceID); err != nil {
		Error(w, http.StatusBadRequest, "invalid invoiceID")
		return
	}
	if _, err := uuid.Parse(sendID); err != nil {
		Error(w, http.StatusBadRequest, "invalid sendID")
		return
	}
	if h.suppression == nil {
		Error(w, http.StatusServiceUnavailable, "Amazon SES is not configured on this server")
		return
	}

	var recipient, provider, status string
	var hardBounced bool
	err := h.db.QueryRow(ctx,
		`SELECT s.recipient, s.provider, s.status,
		        EXISTS (SELECT 1 FROM mail_delivery_events e
		                 WHERE e.send_id = s.id AND e.event_type = 'bounce'
		                   AND e.bounce_type = 'Permanent')
		   FROM email_sends s
		  WHERE s.id = $1 AND s.club_id = $2
		    AND s.related_type = 'invoice' AND s.related_id = $3`,
		sendID, claims.ClubID, invoiceID,
	).Scan(&recipient, &provider, &status, &hardBounced)
	if errors.Is(err, pgx.ErrNoRows) {
		Error(w, http.StatusNotFound, "send not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("load email send")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !canUnsuppress(provider, status, hardBounced) {
		Error(w, http.StatusConflict, "this send was not stopped by the suppression list")
		return
	}

	removed, err := h.suppression.Remove(ctx, recipient)
	if err != nil {
		h.log.Error().Err(err).Str("send_id", sendID).Msg("remove address from SES suppression list")
		Error(w, http.StatusBadGateway, "Amazon SES did not remove the address; try again later")
		return
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionInvoiceRecipientUnsuppressed, "invoice", invoiceID,
			map[string]any{
				"send_id":         sendID,
				"recipient_email": recipient,
				"was_listed":      removed,
			})
	}
	JSON(w, http.StatusOK, map[string]any{
		"removed":         removed,
		"recipient_email": recipient,
	})
}
