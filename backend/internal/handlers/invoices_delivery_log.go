package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type deliveryLogEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	Destination string    `json:"destination,omitempty"`
	SMTPCode    int       `json:"smtp_code,omitempty"`
	Raw         string    `json:"raw"`
}

// HandleGetDeliveryLog reads stalwart-mail's systemd journal and returns the
// most recent delivery attempts whose log line mentions the invoice's
// recipient email. Brygge and Stalwart are colocated, so this is a local read
// — no SSH, no Stalwart admin API.
//
// The destination MX's SMTP response code is the smoking gun:
//   - 2xx: the destination accepted the message (inbox vs spam is then the
//     destination's choice and not visible to the sender)
//   - 4xx: transient, Stalwart will retry
//   - 5xx: permanent failure
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

	var recipient string
	if err := h.db.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(i.recipient_email, ''), u.email, '')
		   FROM invoices i
		   LEFT JOIN users u ON u.id = i.user_id
		  WHERE i.id = $1 AND i.club_id = $2`,
		invoiceID, claims.ClubID,
	).Scan(&recipient); err != nil {
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
			"items":  []deliveryLogEntry{},
			"reason": "no_recipient",
		})
		return
	}

	entries, err := readStalwartDeliveryLog(ctx, recipient, 50)
	if err != nil {
		h.log.Error().Err(err).Str("recipient", recipient).Msg("read stalwart delivery log")
		Error(w, http.StatusInternalServerError, "could not read delivery log")
		return
	}

	if h.audit != nil {
		actorIP := r.Header.Get("X-Real-IP")
		if actorIP == "" {
			actorIP = r.RemoteAddr
		}
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, actorIP,
			audit.ActionInvoiceDeliveryLogViewed, "invoice", invoiceID,
			map[string]any{
				"recipient_email":  recipient,
				"matches_returned": len(entries),
			})
	}

	JSON(w, http.StatusOK, map[string]any{
		"items":           entries,
		"recipient_email": recipient,
	})
}

// smtpCodeRE matches a 3-digit SMTP reply code (2xx, 4xx, 5xx). Anchored to a
// word boundary so we don't pick up port numbers or random digit runs.
var smtpCodeRE = regexp.MustCompile(`\b([245]\d{2})\b`)

// destinationRE matches the destination MX hostname in shapes Stalwart logs
// it as: "via mx.host.tld", "destination=mx.host.tld", or "to mx.host.tld:25".
var destinationRE = regexp.MustCompile(`(?:via|destination[=:]|to)\s+([A-Za-z0-9][A-Za-z0-9.-]+\.[A-Za-z]{2,})`)

// readStalwartDeliveryLog shells out to journalctl (no shell, arg-vector
// form), reads JSON-per-line newest-first via --reverse, filters by recipient,
// and returns up to limit entries. Times out after 8s so a slow journal can't
// hang the request.
func readStalwartDeliveryLog(ctx context.Context, recipient string, limit int) ([]deliveryLogEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx,
		"journalctl",
		"-u", "stalwart-mail.service",
		"--since=60 days ago",
		"--reverse",
		"-o", "json",
		"--no-pager",
		"--output-fields=__REALTIME_TIMESTAMP,MESSAGE",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	type journalRow struct {
		Timestamp string `json:"__REALTIME_TIMESTAMP"`
		Message   string `json:"MESSAGE"`
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1<<16), 1<<20)
	entries := make([]deliveryLogEntry, 0, limit)
	for scanner.Scan() {
		var row journalRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			continue
		}
		if !strings.Contains(row.Message, recipient) {
			continue
		}
		entry := deliveryLogEntry{Raw: strings.TrimSpace(row.Message)}
		if usec, perr := strconv.ParseInt(row.Timestamp, 10, 64); perr == nil {
			entry.Timestamp = time.Unix(0, usec*1000).UTC()
		}
		if m := smtpCodeRE.FindString(row.Message); m != "" {
			if code, perr := strconv.Atoi(m); perr == nil {
				entry.SMTPCode = code
			}
		}
		if m := destinationRE.FindStringSubmatch(row.Message); len(m) > 1 {
			entry.Destination = m[1]
		}
		entries = append(entries, entry)
		if len(entries) >= limit {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}
