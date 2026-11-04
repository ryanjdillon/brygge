package handlers

import (
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// Delivery-log statuses for a send that has no matched submission.
const (
	// deliveryNoRecord: the send is old enough that the mail server must have
	// logged it, and it did not. Earlier mail faults looked exactly like this
	// (shown as sent in Brygge, never left the host).
	deliveryNoRecord = "no_record"
	// deliveryPending: too recent to call missing.
	deliveryPending = "pending"
	// deliveryBeyondRetention: older than the journal keeps.
	deliveryBeyondRetention = "beyond_retention"
	// deliveryUnavailable: this host has no mail-log helper, or it failed.
	deliveryUnavailable = "unavailable"
)

const (
	// sendMatchWindow is how far a send's recorded time may lie from the
	// mail server's submission time. sent_at is stamped just before or just
	// after the SMTP hand-off and audit rows just after, so seconds apart in
	// practice; a minute-scale window absorbs a slow SMTP session.
	sendMatchWindow = 3 * time.Minute
	// sendNoRecordAfter is when a send without a submission becomes
	// "no record" instead of "pending".
	sendNoRecordAfter = 10 * time.Minute
	// mailJournalRetention mirrors journald's MaxRetentionSec on the host.
	mailJournalRetention = 30 * 24 * time.Hour
)

type deliveryLogEvent struct {
	Timestamp time.Time `json:"timestamp"`
	// Type is the SES event type (send, delivery, bounce, ...); empty for a
	// journal event, whose Message is Stalwart's own description.
	Type     string `json:"type,omitempty"`
	Subtype  string `json:"subtype,omitempty"`
	Message  string `json:"message"`
	Hostname string `json:"hostname,omitempty"`
	SMTPCode int    `json:"smtp_code,omitempty"`
	Details  string `json:"details,omitempty"`
}

// deliveryLogSend is one time Brygge mailed the faktura (first send, resend
// or reminder) and what the mail server did with it.
type deliveryLogSend struct {
	// ID is the email_sends row; empty for a send known only from the
	// faktura and the audit log (before the send log existed).
	ID          string             `json:"id,omitempty"`
	Kind        string             `json:"kind"`                // sent | resent | reminder
	Provider    string             `json:"provider,omitempty"`  // ses | stalwart, when ID is set
	Recipient   string             `json:"recipient,omitempty"` // the address this send went to, when ID is set
	At          time.Time          `json:"at"`
	Status      string             `json:"status"`
	QueueID     string             `json:"queue_id,omitempty"`
	SMTPCode    int                `json:"smtp_code,omitempty"`
	RemoteHost  string             `json:"remote_host,omitempty"`
	Details     string             `json:"details,omitempty"`
	OutcomeAt   *time.Time         `json:"outcome_at,omitempty"`
	NextRetryAt *time.Time         `json:"next_retry_at,omitempty"`
	Events      []deliveryLogEvent `json:"events"`
	// CanUnsuppress offers "remove from suppression list" for this send.
	CanUnsuppress bool `json:"can_unsuppress,omitempty"`
}

// HandleGetDeliveryLog lists each time the faktura was mailed and what the
// recipient's mail server answered.
//
// Sends with an email_sends row come from that table: an SES send carries
// the outcome from its delivery events, a Stalwart send is matched against
// the mail server's journal. Sends from before the faktura's first row are
// known only from sent_at and the audit log and are matched against the
// journal through the host's mail-log helper. A journal send with no
// matching submission is reported as no_record, so a mail that never left
// the host is visible instead of looking like an empty log.
//
// The journal path is read-only legacy: it goes away with the brygge-mail-log
// units once every faktura send it could cover is older than the journal
// keeps (BRY-594).
func (h *InvoiceHandler) HandleGetDeliveryLog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	invoiceID := chi.URLParam(r, "invoiceID")
	if invoiceID == "" {
		Error(w, http.StatusBadRequest, "invoiceID required")
		return
	}

	// Same address precedence as the send paths (deliverTo).
	var recipient string
	var sentAt *time.Time
	if err := h.db.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(i.recipient_email, ''), NULLIF(i.member_email, ''), u.email, ''),
		        i.sent_at
		   FROM invoices i
		   LEFT JOIN users u ON u.id = i.user_id
		  WHERE i.id = $1 AND i.club_id = $2`,
		invoiceID, claims.ClubID,
	).Scan(&recipient, &sentAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			Error(w, http.StatusNotFound, "invoice not found")
			return
		}
		h.log.Error().Err(err).Msg("load invoice recipient")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if recipient == "" {
		JSON(w, http.StatusOK, map[string]any{
			"sends":  []deliveryLogSend{},
			"reason": "no_recipient",
		})
		return
	}

	legacy, err := h.invoiceSends(r, invoiceID, claims.ClubID, sentAt)
	if err != nil {
		h.log.Error().Err(err).Msg("load invoice sends")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	rows, rowEvents, err := h.loadTableSends(ctx, claims.ClubID, invoiceID)
	if err != nil {
		h.log.Error().Err(err).Msg("load invoice email sends")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	sends, journalIdx := mergeSends(legacy, rows, rowEvents)

	available := true
	var events []mailEvent
	if len(journalIdx) > 0 {
		available = h.mailJournal != nil
		if available {
			events, err = h.mailJournal.Events(ctx, recipient)
			if err != nil {
				h.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("read mail delivery events")
				available = false
			}
		}
		journalSends := make([]deliveryLogSend, len(journalIdx))
		for k, i := range journalIdx {
			journalSends[k] = sends[i]
		}
		if available {
			matchSends(journalSends, groupSubmissions(events), time.Now())
		} else {
			for k := range journalSends {
				journalSends[k].Status = deliveryUnavailable
			}
		}
		for k, i := range journalIdx {
			sends[i] = journalSends[k]
		}
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionInvoiceDeliveryLogViewed, "invoice", invoiceID,
			map[string]any{
				"recipient_email":  recipient,
				"matches_returned": len(events),
				"logged_sends":     len(rows),
			})
	}

	sort.SliceStable(sends, func(i, j int) bool { return sends[i].At.After(sends[j].At) })
	JSON(w, http.StatusOK, map[string]any{
		"sends":           sends,
		"recipient_email": recipient,
		"available":       available,
	})
}

// invoiceSends lists the moments Brygge mailed the faktura: the first send
// (sent_at) and each resend and reminder (audit rows). Imported fakturas sent
// by the previous system have no sends.
func (h *InvoiceHandler) invoiceSends(r *http.Request, invoiceID, clubID string, sentAt *time.Time) ([]deliveryLogSend, error) {
	sends := []deliveryLogSend{}
	if sentAt != nil {
		sends = append(sends, deliveryLogSend{Kind: "sent", At: sentAt.UTC()})
	}
	rows, err := h.db.Query(r.Context(),
		`SELECT CASE WHEN action = 'invoice.reminded' THEN 'reminder' ELSE 'resent' END,
		        created_at
		   FROM audit_log
		  WHERE club_id = $1 AND resource = 'invoice' AND resource_id = $2
		    AND (action = 'invoice.reminded'
		         OR (action = 'invoice.emailed' AND details->>'resend' = 'true'))`,
		clubID, invoiceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s deliveryLogSend
		if err := rows.Scan(&s.Kind, &s.At); err != nil {
			return nil, err
		}
		s.At = s.At.UTC()
		sends = append(sends, s)
	}
	return sends, rows.Err()
}

// mergeSends joins the journal-era sends (sent_at and audit rows) with the
// faktura's email_sends rows into one list and returns the indexes of the
// entries the journal must decide.
//
// Once a faktura has rows, the send paths that write them also stamp sent_at
// and the audit log, so a journal-era send at or after the first row (less
// the match window, since sent_at can be stamped a moment before the row) is
// the same mail as a row and is dropped. A Stalwart row (EMAIL_PROVIDER=smtp
// or the magic-link fallback) is still matched against the journal; an SES
// row is not.
func mergeSends(legacy []deliveryLogSend, rows []tableSend, rowEvents map[string][]deliveryLogEvent) ([]deliveryLogSend, []int) {
	var kept []deliveryLogSend
	if len(rows) == 0 {
		kept = legacy
	} else {
		cutover := rows[0].createdAt.Add(-sendMatchWindow)
		for _, s := range legacy {
			if s.At.Before(cutover) {
				kept = append(kept, s)
			}
		}
	}
	var journalIdx []int
	sends := make([]deliveryLogSend, 0, len(kept)+len(rows))
	firstSent := false
	for _, s := range kept {
		if s.Kind != "reminder" {
			firstSent = true
		}
		journalIdx = append(journalIdx, len(sends))
		sends = append(sends, s)
	}
	for _, row := range rows {
		kind := deliveryKind(row.kind, !firstSent)
		if kind != "reminder" {
			firstSent = true
		}
		s, needsJournal := fromTableSend(row, rowEvents[row.id], kind)
		if needsJournal {
			journalIdx = append(journalIdx, len(sends))
		}
		sends = append(sends, s)
	}
	return sends, journalIdx
}

// matchSends pairs each send with the closest unclaimed submission within
// sendMatchWindow, closest pairs first, and fills in its outcome. Other mail
// to the same address (a login link, a board reply) falls outside every
// window and is not shown.
func matchSends(sends []deliveryLogSend, subs []*mailSubmission, now time.Time) {
	type pair struct {
		send, sub int
		gap       time.Duration
	}
	var pairs []pair
	for i := range sends {
		for j, sub := range subs {
			gap := sub.SubmittedAt.Sub(sends[i].At)
			if gap < 0 {
				gap = -gap
			}
			if gap <= sendMatchWindow {
				pairs = append(pairs, pair{i, j, gap})
			}
		}
	}
	sort.SliceStable(pairs, func(a, b int) bool { return pairs[a].gap < pairs[b].gap })
	sendUsed := make([]bool, len(sends))
	subUsed := make([]bool, len(subs))
	for _, p := range pairs {
		if sendUsed[p.send] || subUsed[p.sub] {
			continue
		}
		sendUsed[p.send], subUsed[p.sub] = true, true
		fillSend(&sends[p.send], subs[p.sub])
	}
	for i := range sends {
		if sendUsed[i] {
			continue
		}
		sends[i].Events = []deliveryLogEvent{}
		age := now.Sub(sends[i].At)
		switch {
		case age > mailJournalRetention:
			sends[i].Status = deliveryBeyondRetention
		case age < sendNoRecordAfter:
			sends[i].Status = deliveryPending
		default:
			sends[i].Status = deliveryNoRecord
		}
	}
}

func fillSend(s *deliveryLogSend, sub *mailSubmission) {
	out := sub.outcome()
	s.Status = out.Status
	s.QueueID = sub.QueueID
	s.SMTPCode = out.SMTPCode
	s.RemoteHost = out.RemoteHost
	s.Details = out.Detail
	s.OutcomeAt = out.At
	s.NextRetryAt = out.NextRetryAt
	s.Events = make([]deliveryLogEvent, 0, len(sub.Events))
	for _, e := range sub.Events {
		s.Events = append(s.Events, deliveryLogEvent{
			Timestamp: e.at(),
			Message:   e.Message,
			Hostname:  e.Hostname,
			SMTPCode:  e.code(),
			Details:   e.detail(),
		})
	}
}
