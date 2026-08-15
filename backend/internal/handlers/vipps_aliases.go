package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// HandleListCustomerAliases GET /admin/vipps/customer-aliases
func (h *AccountingHandler) HandleListCustomerAliases(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	aliases, err := h.svc.ListCustomerAliases(r.Context(), claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("list customer aliases")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if aliases == nil {
		aliases = []accounting.VippsCustomerAlias{}
	}
	JSON(w, http.StatusOK, aliases)
}

// HandleUpsertCustomerAlias POST /admin/vipps/customer-aliases
func (h *AccountingHandler) HandleUpsertCustomerAlias(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req struct {
		CustomerName string `json:"customer_name"`
		UserID       string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CustomerName == "" || req.UserID == "" {
		Error(w, http.StatusBadRequest, "customer_name and user_id required")
		return
	}
	a, err := h.svc.UpsertCustomerAlias(r.Context(), claims.ClubID, req.CustomerName, req.UserID)
	if err != nil {
		h.log.Error().Err(err).Msg("upsert customer alias")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, a)
}

// HandleDeleteCustomerAlias DELETE /admin/vipps/customer-aliases/{aliasID}
func (h *AccountingHandler) HandleDeleteCustomerAlias(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id := chi.URLParam(r, "aliasID")
	if err := h.svc.DeleteCustomerAlias(r.Context(), claims.ClubID, id); err != nil {
		h.log.Error().Err(err).Msg("delete customer alias")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleListMessageRules GET /admin/vipps/message-rules
func (h *AccountingHandler) HandleListMessageRules(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rules, err := h.svc.ListMessageRules(r.Context(), claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("list message rules")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if rules == nil {
		rules = []accounting.VippsMessageRule{}
	}
	JSON(w, http.StatusOK, rules)
}

// HandleUpsertMessageRule POST /admin/vipps/message-rules
func (h *AccountingHandler) HandleUpsertMessageRule(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req struct {
		Pattern     string `json:"pattern"`
		AccountCode string `json:"account_code"`
		Priority    int    `json:"priority"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Pattern == "" || req.AccountCode == "" {
		Error(w, http.StatusBadRequest, "pattern and account_code required")
		return
	}
	rule, err := h.svc.UpsertMessageRule(r.Context(), claims.ClubID, req.Pattern, req.AccountCode, req.Priority)
	if err != nil {
		h.log.Error().Err(err).Msg("upsert message rule")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, rule)
}

// HandleDeleteMessageRule DELETE /admin/vipps/message-rules/{ruleID}
func (h *AccountingHandler) HandleDeleteMessageRule(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id := chi.URLParam(r, "ruleID")
	if err := h.svc.DeleteMessageRule(r.Context(), claims.ClubID, id); err != nil {
		h.log.Error().Err(err).Msg("delete message rule")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
