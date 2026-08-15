package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/brygge-klubb/brygge/internal/accounting"
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

	ledgerID := req.LedgerID
	if ledgerID == "" {
		ledgers, err := h.vipps.LedgersParsed(ctx)
		if err != nil || len(ledgers) == 0 {
			h.log.Error().Err(err).Msg("vipps report sync: resolve ledger failed")
			Error(w, http.StatusBadGateway, "could not resolve Vipps ledger")
			return
		}
		ledgerID = ledgers[0].LedgerID
	}

	funds, fundsTryLater, err := h.vipps.FundsForDate(ctx, ledgerID, req.Date)
	if err != nil {
		h.log.Error().Err(err).Str("date", req.Date).Msg("vipps report sync: funds failed")
		Error(w, http.StatusBadGateway, "Vipps funds report failed")
		return
	}
	fees, feesTryLater, err := h.vipps.FeesForDate(ctx, ledgerID, req.Date)
	if err != nil {
		h.log.Error().Err(err).Str("date", req.Date).Msg("vipps report sync: fees failed")
		Error(w, http.StatusBadGateway, "Vipps fees report failed")
		return
	}
	if fundsTryLater || feesTryLater {
		JSON(w, http.StatusAccepted, map[string]any{
			"status":  "try_later",
			"message": "Vipps report for this date is not finalised yet — try again later",
		})
		return
	}

	rows := accounting.VippsReportRows(h.vipps.MSN(), funds, fees)
	if len(rows) == 0 {
		JSON(w, http.StatusOK, map[string]any{
			"imported":    0,
			"skipped_dup": 0,
			"message":     "no settled transactions for this date",
		})
		return
	}

	var importID string
	if err := h.svc.DB().QueryRow(ctx,
		`INSERT INTO vipps_imports (club_id, filename, msn, imported_by, row_count)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		claims.ClubID, "Vipps Report API "+req.Date, h.vipps.MSN(), claims.UserID, len(rows),
	).Scan(&importID); err != nil {
		h.log.Error().Err(err).Msg("vipps report sync: create import failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	result, err := h.svc.ImportVippsRows(ctx, claims.ClubID, importID, rows)
	if err != nil {
		h.log.Error().Err(err).Msg("vipps report sync: import rows failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.svc.DB().Exec(ctx,
		`UPDATE vipps_imports SET row_count = $1 WHERE id = $2`,
		result.Imported, importID)

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.vipps_report_synced", "vipps_import", importID,
			map[string]any{
				"date":        req.Date,
				"ledger_id":   ledgerID,
				"imported":    result.Imported,
				"skipped_dup": result.SkippedDup,
			})
	}

	JSON(w, http.StatusOK, map[string]any{
		"imported":    result.Imported,
		"skipped_dup": result.SkippedDup,
		"import_id":   importID,
	})
}
