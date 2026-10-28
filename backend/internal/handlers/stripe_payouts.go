package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// stripeBusyCode is the 409 code while another Stripe sync or booking of
// the club holds its slot.
const stripeBusyCode = "busy"

// HandleListStripePayouts is GET /accounting/stripe/payouts?cleared=false:
// the club's paid NOK payouts not yet cleared, for the clearing picker.
// Only uncleared payouts are listed; cleared=true is refused rather than
// silently answered with the uncleared ones.
func (h *AccountingHandler) HandleListStripePayouts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if c := r.URL.Query().Get("cleared"); c != "" && c != "false" {
		Error(w, http.StatusBadRequest, "only cleared=false is supported")
		return
	}
	items, err := h.svc.ListUnclearedStripePayouts(ctx, claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("listing stripe payouts failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"items": items})
}

type clearStripePayoutReq struct {
	BankRowID string `json:"bank_row_id"`
}

// HandleClearStripePayout is POST /accounting/stripe/payouts/{payoutID}/clear
// with {bank_row_id}: a person clears the payout against that deposit
// (accounting.ClearStripePayoutManually). A refusal is 409 with the
// refusal's code (open_members for members_open, open_balance for
// balance_open), a Stripe slot still busy after a short wait 409 busy,
// an unknown payout or row 404. Audited stripe_payout.cleared.
func (h *AccountingHandler) HandleClearStripePayout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	payoutRowID := chi.URLParam(r, "payoutID")
	var req clearStripePayoutReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.BankRowID == "" {
		Error(w, http.StatusBadRequest, "bank_row_id required")
		return
	}
	if _, err := uuid.Parse(payoutRowID); err != nil {
		Error(w, http.StatusNotFound, accounting.ErrStripePayoutNotFound.Error())
		return
	}
	if _, err := uuid.Parse(req.BankRowID); err != nil {
		Error(w, http.StatusNotFound, accounting.ErrBankRowNotFound.Error())
		return
	}

	res, err := h.svc.ClearStripePayoutManually(ctx, claims.ClubID, claims.UserID, payoutRowID, req.BankRowID)
	var refusal *accounting.StripeClearRefusal
	switch {
	case errors.As(err, &refusal):
		body := map[string]any{"error": refusal.Error(), "code": refusal.Code}
		if refusal.Reason != "" {
			body["reason"] = refusal.Reason
		}
		switch refusal.Code {
		case accounting.StripeClearMembersOpen:
			body["open_members"] = refusal.OpenMembers
		case accounting.StripeClearBalanceOpen:
			body["open_balance"] = refusal.OpenBalance
		}
		JSON(w, http.StatusConflict, body)
		return
	case errors.Is(err, accounting.ErrStripeSyncBusy):
		JSON(w, http.StatusConflict, map[string]string{
			"error": "A Stripe sync is already running; try again shortly", "code": stripeBusyCode,
		})
		return
	case errors.Is(err, accounting.ErrStripePayoutNotFound), errors.Is(err, accounting.ErrBankRowNotFound):
		Error(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		h.log.Error().Err(err).Str("payout", payoutRowID).Str("bank_row_id", req.BankRowID).Msg("manual stripe payout clearing failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if h.audit != nil {
		details := map[string]any{
			"payout_id":        res.PayoutID,
			"bank_row_id":      res.BankRowID,
			"journal_entry_id": res.JournalEntryID,
			"manual":           true,
			"members_count":    res.MembersCount,
			"members_net_ore":  res.MembersNetOre,
		}
		if res.ClearingBalanceOre != nil {
			details["clearing_balance_ore"] = *res.ClearingBalanceOre
		}
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionStripePayoutCleared, "stripe_payout", res.PayoutRowID,
			details)
	}
	JSON(w, http.StatusOK, res)
}
