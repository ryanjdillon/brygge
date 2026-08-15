package handlers

import (
	"net/http"
	"strconv"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// HandleListVippsQueue GET /admin/accounting/vipps/queue
// Returns unmatched belastning rows for the Vipps review queue.
func (h *AccountingHandler) HandleListVippsQueue(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	rows, err := h.svc.ListVippsQueue(r.Context(), claims.ClubID, limit)
	if err != nil {
		h.log.Error().Err(err).Msg("list vipps queue")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if rows == nil {
		rows = []accounting.VippsQueueRow{}
	}
	JSON(w, http.StatusOK, rows)
}
