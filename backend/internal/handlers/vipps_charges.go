package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// Per-charge Vipps reconciliation endpoints (BRY-227). These back the Tildel
// tab's individual Vipps charge items: one line per payer instead of a single
// all-or-nothing payout bilag. The write paths call the postings core in
// accounting/vippspostings.go (BRY-226).

// HandleListUnreconciledVippsCharges returns the un-journaled Vipps charges as
// unified reconcile items for the Tildel queue.
func (h *AccountingHandler) HandleListUnreconciledVippsCharges(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	items, err := h.svc.ListUnreconciledVippsCharges(ctx, claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("list unreconciled vipps charges")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"items": items})
}

// HandleAssignVippsChargeToInvoice books a charge against an open faktura
// (DR clearing / CR 1500) and back-links the invoice payment.
func (h *AccountingHandler) HandleAssignVippsChargeToInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rowID := chi.URLParam(r, "rowID")
	var req assignInvoiceReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.InvoiceID == "" {
		Error(w, http.StatusBadRequest, "invoice_id required")
		return
	}
	res, err := h.svc.AssignVippsChargeToInvoice(ctx, claims.ClubID, rowID, req.InvoiceID, claims.UserID)
	if err != nil {
		h.log.Warn().Err(err).Str("row_id", rowID).Msg("assign vipps charge to invoice")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionVippsChargeAssignedInvoice, "vipps_import_row", rowID,
			map[string]any{
				"invoice_id":       req.InvoiceID,
				"journal_entry_id": res.EntryID,
			})
	}
	JSON(w, http.StatusOK, res)
}

// HandleCreateVippsChargeBilag books a walk-up charge to a chosen account
// (DR clearing / CR <account>).
func (h *AccountingHandler) HandleCreateVippsChargeBilag(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rowID := chi.URLParam(r, "rowID")
	var req assignAccountReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AccountCode == "" {
		Error(w, http.StatusBadRequest, "account_code required")
		return
	}
	res, err := h.svc.CreateVippsChargeBilag(ctx, claims.ClubID, rowID, req.AccountCode, req.Description, claims.UserID)
	if err != nil {
		h.log.Warn().Err(err).Str("row_id", rowID).Msg("create vipps charge bilag")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionVippsChargeBilag, "vipps_import_row", rowID,
			map[string]any{
				"account_code":     req.AccountCode,
				"description":      req.Description,
				"journal_entry_id": res.EntryID,
			})
	}
	JSON(w, http.StatusOK, res)
}

// HandleDismissVippsCharge marks a charge avvist so it leaves the queue and no
// longer blocks its settlement's payout auto-clear.
func (h *AccountingHandler) HandleDismissVippsCharge(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rowID := chi.URLParam(r, "rowID")
	var req dismissReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Reason == "" {
		Error(w, http.StatusBadRequest, "reason required")
		return
	}
	if !accounting.IsValidDismissReason(req.Reason) {
		Error(w, http.StatusBadRequest, "invalid reason")
		return
	}
	if err := h.svc.DismissVippsCharge(ctx, claims.ClubID, claims.UserID, rowID, req.Reason); err != nil {
		h.log.Warn().Err(err).Str("row_id", rowID).Msg("dismiss vipps charge")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionVippsChargeDismissed, "vipps_import_row", rowID,
			map[string]any{"reason": req.Reason})
	}
	JSON(w, http.StatusOK, map[string]string{"status": "dismissed"})
}

// HandleClearVippsPayout auto-clears a Vipps settlement payout bank row once
// every charge in the settlement is booked (books the fee + bank entries).
// When charges remain it responds 200 with ready=false and the remaining
// count so the UI can render the quiet auto-clearing item.
func (h *AccountingHandler) HandleClearVippsPayout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rowID := chi.URLParam(r, "rowID")
	res, err := h.svc.ClearVippsPayout(ctx, claims.ClubID, rowID, claims.UserID)
	if err != nil {
		h.log.Warn().Err(err).Str("bank_row_id", rowID).Msg("clear vipps payout")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.audit != nil && res.Ready && !res.AlreadyCleared {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionVippsPayoutCleared, "bank_import_row", rowID,
			map[string]any{
				"settlement":       res.SettlementNumber,
				"fee_entry_id":     res.FeeEntryID,
				"bank_entry_id":    res.BankEntryID,
				"clearing_balance": res.ClearingBalance,
			})
	}
	JSON(w, http.StatusOK, res)
}
