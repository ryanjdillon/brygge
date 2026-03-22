package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/email"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type BroadcastHandler struct {
	db     *pgxpool.Pool
	config *config.Config
	email  *email.Client
	log    zerolog.Logger
}

func NewBroadcastHandler(
	db *pgxpool.Pool,
	cfg *config.Config,
	emailClient *email.Client,
	log zerolog.Logger,
) *BroadcastHandler {
	return &BroadcastHandler{
		db:     db,
		config: cfg,
		email:  emailClient,
		log:    log.With().Str("handler", "broadcast").Logger(),
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

	h.log.Info().
		Str("subject", req.Subject).
		Str("recipients", req.Recipients).
		Str("sent_by", claims.UserID).
		Msg("broadcast sent (stub — Resend integration pending)")

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
