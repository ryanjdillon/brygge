package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/brygge-klubb/brygge/internal/middleware"
)

type vippsReportSyncRequest struct {
	Date     string `json:"date"`      // YYYY-MM-DD (settlement date)
	LedgerID string `json:"ledger_id"` // optional; resolved from the merchant's ledgers when empty
}

// HandleVippsReportSync pulls the Vipps Report API v2 funds + fees for a date
// and ingests them through the same idempotent pipeline as the Vipps CSV export
// (BRY-4). Rows land in the vipps_import_rows review queue (Vipps-kø), carrying
// the merchant reference for later auto-linking. Re-running for the same date is
// idempotent (row_hash dedup).
func (h *AccountingHandler) HandleVippsReportSync(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if h.vipps == nil {
		Error(w, http.StatusServiceUnavailable, "Vipps is not configured")
		return
	}

	var req vippsReportSyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Date == "" {
		Error(w, http.StatusBadRequest, "date is required (YYYY-MM-DD)")
		return
	}

	res, err := h.svc.SyncVippsReport(ctx, h.vipps, claims.ClubID, claims.UserID, req.LedgerID, req.Date)
	if err != nil {
		h.log.Error().Err(err).Str("date", req.Date).Msg("vipps report sync failed")
		Error(w, http.StatusBadGateway, "Vipps report sync failed")
		return
	}
	if res.TryLater {
		JSON(w, http.StatusAccepted, map[string]any{
			"status":  "try_later",
			"message": "Vipps report for this date is not finalised yet — try again later",
		})
		return
	}
	if res.Rows == 0 {
		JSON(w, http.StatusOK, map[string]any{
			"imported":    0,
			"skipped_dup": 0,
			"message":     "no settled transactions for this date",
		})
		return
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.vipps_report_synced", "vipps_import", res.ImportID,
			map[string]any{
				"date":        req.Date,
				"ledger_id":   res.LedgerID,
				"imported":    res.Imported,
				"skipped_dup": res.SkippedDup,
			})
	}

	JSON(w, http.StatusOK, map[string]any{
		"imported":    res.Imported,
		"skipped_dup": res.SkippedDup,
		"import_id":   res.ImportID,
		"orders":      res.Orders,
		"auto_assign": res.AutoAssign,
	})
}
