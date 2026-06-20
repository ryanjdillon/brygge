package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	bcast "github.com/brygge-klubb/brygge/internal/broadcast"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// BroadcastsHandler serves the bulk-send history surface behind the inbox
// Broadcasts tab: a list with aggregate delivery counts, per-recipient
// detail, and a retry that re-queues failed deliveries. Read paths are
// club-scoped in the query layer.
type BroadcastsHandler struct {
	store *bcast.Store
	// kick nudges the delivery worker after a retry re-queues rows. May
	// be nil (the queue still drains on the worker's next tick).
	kick func()
	log  zerolog.Logger
}

// NewBroadcastsHandler builds the handler. kick is the worker nudge (or nil).
func NewBroadcastsHandler(db *pgxpool.Pool, kick func(), log zerolog.Logger) *BroadcastsHandler {
	return &BroadcastsHandler{
		store: bcast.NewStore(db),
		kick:  kick,
		log:   log.With().Str("handler", "broadcasts").Logger(),
	}
}

// HandleList returns the club's broadcasts newest-first with delivery counts.
//
//	GET /admin/broadcasts
func (h *BroadcastsHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	list, err := h.store.List(r.Context(), claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("list broadcasts failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"broadcasts": list})
}

// HandleGet returns one broadcast with its per-recipient delivery rows.
//
//	GET /admin/broadcasts/{id}
func (h *BroadcastsHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	d, err := h.store.Get(r.Context(), claims.ClubID, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			Error(w, http.StatusNotFound, "broadcast not found")
			return
		}
		h.log.Error().Err(err).Str("id", id).Msg("get broadcast failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, d)
}

// HandleRetry re-queues a broadcast's terminally-failed deliveries and
// nudges the worker. Club-scoped; fresh-TOTP gated at the route level.
//
//	POST /admin/broadcasts/{id}/retry
func (h *BroadcastsHandler) HandleRetry(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id := chi.URLParam(r, "id")
	n, err := h.store.RequeueFailed(r.Context(), claims.ClubID, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			Error(w, http.StatusNotFound, "broadcast not found")
			return
		}
		h.log.Error().Err(err).Str("id", id).Msg("retry broadcast failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if n > 0 && h.kick != nil {
		h.kick()
	}
	JSON(w, http.StatusOK, map[string]any{"requeued": n})
}
