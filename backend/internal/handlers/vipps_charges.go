package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

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

// vippsChargeIDParam reads {rowID}; an id that is not a uuid is answered 404
// rather than reaching Postgres as an invalid uuid (a 500).
func vippsChargeIDParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "rowID")
	if _, err := uuid.Parse(id); err != nil {
		Error(w, http.StatusNotFound, accounting.ErrVippsChargeNotFound.Error())
		return "", false
	}
	return id, true
}

// HandleVippsChargePotentialInvoices lists open unpaid invoices whose amount
// matches the charge — the candidate list for Tildel→faktura. A charge that
// is not the club's, or an id that is not a uuid, is 404, like the
// suggestions route.
func (h *AccountingHandler) HandleVippsChargePotentialInvoices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rowID, ok := vippsChargeIDParam(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.svc.PotentialInvoicesForVippsCharge(ctx, claims.ClubID, rowID, r.URL.Query().Get("q"), limit)
	if errors.Is(err, accounting.ErrVippsChargeNotFound) {
		Error(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		h.log.Error().Err(err).Str("row_id", rowID).Msg("vipps charge potential invoices")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"items": items, "potential": true})
}

// HandleVippsChargeSuggestions ranks fakturas and accounts for one Vipps
// charge, the same suggestions a bank row gets.
func (h *AccountingHandler) HandleVippsChargeSuggestions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rowID, ok := vippsChargeIDParam(w, r)
	if !ok {
		return
	}
	res, err := h.svc.VippsChargeSuggestionsFor(ctx, claims.ClubID, rowID)
	if errors.Is(err, accounting.ErrVippsChargeNotFound) {
		Error(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		h.log.Error().Err(err).Str("row_id", rowID).Msg("vipps charge suggestions")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, res)
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
	var req struct {
		InvoiceID string `json:"invoice_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.InvoiceID == "" {
		Error(w, http.StatusBadRequest, "invoice_id required")
		return
	}
	res, err := h.svc.AssignVippsChargeToInvoice(ctx, claims.ClubID, rowID, req.InvoiceID, claims.UserID)
	if invoiceNotAssignableConflict(w, err) {
		return
	}
	if errors.Is(err, accounting.ErrImportedNoOpeningBalance) {
		importedNoOpeningBalanceConflict(w)
		return
	}
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

// HandleUnassignVippsCharge undoes a charge booking (manual or automatic) so
// the charge returns to the Tildel queue and a faktura it settled reopens.
// With released_from_backlog the charge must be a 1500-parked draft the Vipps
// backlog run refused (BRY-392), in an open period, and the audit row says
// so. The settled-faktura test on such drafts runs either way.
func (h *AccountingHandler) HandleUnassignVippsCharge(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rowID := chi.URLParam(r, "rowID")
	// The body is optional: a plain undo posts none.
	var req struct {
		ReleasedFromBacklog bool `json:"released_from_backlog"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var entryID, invoiceID string
	var err error
	if req.ReleasedFromBacklog {
		entryID, err = h.svc.ReleaseVippsBacklogCharge(ctx, claims.ClubID, claims.UserID, rowID)
	} else {
		entryID, invoiceID, err = h.svc.UnassignVippsCharge(ctx, claims.ClubID, claims.UserID, rowID)
	}
	if errors.Is(err, accounting.ErrVippsChargeInSettlement) {
		JSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "code": "charge_in_settlement"})
		return
	}
	if errors.Is(err, accounting.ErrVippsChargeInClearedSettlement) {
		writeChargeInClearedSettlement(w, err)
		return
	}
	if err != nil {
		h.log.Warn().Err(err).Str("row_id", rowID).Msg("unassign vipps charge")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.audit != nil {
		details := map[string]any{
			"journal_entry_id": entryID,
			"invoice_id":       invoiceID,
		}
		if req.ReleasedFromBacklog {
			details["released_from_backlog"] = true
		}
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionVippsChargeUnassigned, "vipps_import_row", rowID, details)
	}
	JSON(w, http.StatusOK, map[string]string{"status": "unassigned"})
}

// chargeInClearedSettlementCode is the 409 code of a refused unbook of a
// charge whose Vipps settlement is already cleared.
const chargeInClearedSettlementCode = "charge_in_cleared_settlement"

// writeChargeInClearedSettlement answers 409 charge_in_cleared_settlement
// for a charge whose settlement's payout is already cleared against the bank
// deposit (BRY-555). The message is a Norwegian sentence naming the settlement
// and the deposit to undo first; the settlement and the deposit's bank row are
// also given as fields, so the SPA can offer that undo (BankRowID is the row
// whose unassign undoes the clearing).
func writeChargeInClearedSettlement(w http.ResponseWriter, err error) {
	body := map[string]string{"error": err.Error(), "code": chargeInClearedSettlementCode}
	var cleared *accounting.ChargeInClearedSettlementError
	if errors.As(err, &cleared) {
		body["settlement_number"] = cleared.SettlementNumber
		body["msn"] = cleared.MSN
		if cleared.BankRowID != "" {
			body["bank_row_id"] = cleared.BankRowID
		}
	}
	JSON(w, http.StatusConflict, body)
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
