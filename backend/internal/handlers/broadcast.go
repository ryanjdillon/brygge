package handlers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/unsubscribe"
)

type BroadcastHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	email  email.Sender
	log    zerolog.Logger
	secret []byte
}

func NewBroadcastHandler(
	db *pgxpool.Pool,
	cfg *config.Config,
	emailClient email.Sender,
	log zerolog.Logger,
) *BroadcastHandler {
	secret, _ := hex.DecodeString(cfg.TOTPEncryptionKey)
	return &BroadcastHandler{
		db:     db,
		config: cfg,
		email:  emailClient,
		log:    log.With().Str("handler", "broadcast").Logger(),
		secret: secret,
	}
}

type broadcast struct {
	ID         string    `json:"id"`
	ClubID     string    `json:"club_id"`
	Subject    string    `json:"subject"`
	Body       string    `json:"body"`
	Recipients string    `json:"recipients"`
	SentBy     string    `json:"sent_by"`
	SentAt     time.Time `json:"sent_at"`
	CreatedAt  time.Time `json:"created_at"`
}

type sendBroadcastRequest struct {
	Subject    string `json:"subject"`
	Body       string `json:"body"`
	Recipients string `json:"recipients"`
}

func (h *BroadcastHandler) HandleSendBroadcast(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req sendBroadcastRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Subject == "" || req.Body == "" {
		Error(w, http.StatusBadRequest, "subject and body are required")
		return
	}

	validRecipients := map[string]bool{
		"all":          true,
		"members":      true,
		"board":        true,
		"slip_holders": true,
	}
	if !validRecipients[req.Recipients] {
		Error(w, http.StatusBadRequest, "recipients must be one of: all, members, board, slip_holders")
		return
	}

	var b broadcast
	err := h.db.QueryRow(ctx,
		`INSERT INTO broadcasts (club_id, subject, body, recipients, sent_by)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, club_id, subject, body, recipients, sent_by, sent_at, created_at`,
		claims.ClubID, req.Subject, req.Body, req.Recipients, claims.UserID,
	).Scan(
		&b.ID, &b.ClubID, &b.Subject, &b.Body, &b.Recipients,
		&b.SentBy, &b.SentAt, &b.CreatedAt,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to store broadcast")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if h.email != nil {
		go h.deliverBroadcast(b, claims.ClubID)
	}

	JSON(w, http.StatusCreated, b)
}

func (h *BroadcastHandler) HandleListBroadcasts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.db.Query(ctx,
		`SELECT id, club_id, subject, body, recipients, sent_by, sent_at, created_at
		 FROM broadcasts
		 WHERE club_id = $1
		 ORDER BY sent_at DESC`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list broadcasts")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	broadcasts := make([]broadcast, 0)
	for rows.Next() {
		var b broadcast
		if err := rows.Scan(
			&b.ID, &b.ClubID, &b.Subject, &b.Body, &b.Recipients,
			&b.SentBy, &b.SentAt, &b.CreatedAt,
		); err != nil {
			h.log.Error().Err(err).Msg("failed to scan broadcast")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		broadcasts = append(broadcasts, b)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("error iterating broadcast rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, broadcasts)
}

type broadcastRecipient struct {
	UserID string
	Email  string
}

// deliverBroadcast sends the broadcast to all eligible recipients in the
// background. It respects per-user email opt-outs for the "broadcast"
// category and attaches RFC 8058 List-Unsubscribe headers.
func (h *BroadcastHandler) deliverBroadcast(b broadcast, clubID string) {
	ctx := context.Background()

	recipientQuery := `
		SELECT u.id, u.email
		FROM users u
		LEFT JOIN communication_preferences cp
			ON cp.user_id = u.id AND cp.club_id = u.club_id AND cp.category = 'broadcast'
		WHERE u.club_id = $1
		  AND (cp.email_enabled IS NULL OR cp.email_enabled = true)`

	switch b.Recipients {
	case "members":
		recipientQuery += ` AND EXISTS (SELECT 1 FROM slips s WHERE s.member_id = u.id AND s.club_id = u.club_id)`
	case "board":
		recipientQuery += ` AND EXISTS (SELECT 1 FROM user_roles ur WHERE ur.user_id = u.id AND ur.club_id = u.club_id AND ur.role IN ('board', 'admin'))`
	case "slip_holders":
		recipientQuery += ` AND EXISTS (SELECT 1 FROM slips s WHERE s.member_id = u.id AND s.club_id = u.club_id)`
	}

	rows, err := h.db.Query(ctx, recipientQuery, clubID)
	if err != nil {
		h.log.Error().Err(err).Str("broadcast_id", b.ID).Msg("failed to query broadcast recipients")
		return
	}
	defer rows.Close()

	var recipients []broadcastRecipient
	for rows.Next() {
		var rec broadcastRecipient
		if err := rows.Scan(&rec.UserID, &rec.Email); err != nil {
			h.log.Error().Err(err).Msg("failed to scan broadcast recipient")
			return
		}
		recipients = append(recipients, rec)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("broadcast recipient rows error")
		return
	}

	baseURL := fmt.Sprintf("https://%s/api/v1/unsubscribe", h.config.Domain)
	throttle := h.config.BulkSendThrottle

	sent, failed := 0, 0
	for _, rec := range recipients {
		tok := unsubscribe.GenerateToken(rec.UserID, "broadcast", h.secret)
		unsubURL := baseURL + "?token=" + tok + "&category=broadcast"

		headers := map[string]string{
			"List-Unsubscribe":      "<" + unsubURL + ">",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		}

		if err := h.email.SendWithHeaders(ctx, rec.Email, b.Subject, b.Body, headers); err != nil {
			h.log.Error().Err(err).Str("to", rec.Email).Str("broadcast_id", b.ID).Msg("broadcast send failed")
			failed++
		} else {
			sent++
		}

		if throttle > 0 {
			time.Sleep(throttle)
		}
	}

	h.log.Info().
		Str("broadcast_id", b.ID).
		Int("sent", sent).
		Int("failed", failed).
		Int("total", len(recipients)).
		Msg("broadcast delivery complete")
}
