package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

type AccountingHandler struct {
	svc   *accounting.Service
	audit *audit.Service
	log   zerolog.Logger
}

func NewAccountingHandler(svc *accounting.Service, auditService *audit.Service, log zerolog.Logger) *AccountingHandler {
	return &AccountingHandler{
		svc:   svc,
		audit: auditService,
		log:   log.With().Str("handler", "accounting").Logger(),
	}
}

func (h *AccountingHandler) HandleListAccounts(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	accounts, err := h.svc.ListAccounts(r.Context(), claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list accounts")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if accounts == nil {
		accounts = []accounting.Account{}
	}
	JSON(w, http.StatusOK, accounts)
}

func (h *AccountingHandler) HandleSeedAccounts(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	seeded, err := h.svc.SeedKontoplan(r.Context(), claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to seed kontoplan")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.kontoplan_seeded", "accounts", "", map[string]any{"seeded": seeded})
	}

	JSON(w, http.StatusOK, map[string]any{"seeded": seeded})
}

type createAccountRequest struct {
	Code        string                   `json:"code"`
	Name        string                   `json:"name"`
	AccountType accounting.AccountType   `json:"account_type"`
	ParentCode  string                   `json:"parent_code"`
	MVAEligible accounting.MVAEligibility `json:"mva_eligible"`
	Description string                   `json:"description"`
	SortOrder   int                      `json:"sort_order"`
}

func (h *AccountingHandler) HandleCreateAccount(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req createAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Code == "" || req.Name == "" || req.AccountType == "" {
		Error(w, http.StatusBadRequest, "code, name, and account_type are required")
		return
	}

	id, err := h.svc.CreateAccount(r.Context(), claims.ClubID, accounting.AccountDef{
		Code:        req.Code,
		Name:        req.Name,
		Type:        req.AccountType,
		ParentCode:  req.ParentCode,
		MVAEligible: req.MVAEligible,
		Description: req.Description,
		SortOrder:   req.SortOrder,
	})
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create account")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.account_created", "account", id,
			map[string]any{"code": req.Code, "name": req.Name})
	}

	JSON(w, http.StatusCreated, map[string]string{"id": id})
}

type updateAccountRequest struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	MVAEligible accounting.MVAEligibility `json:"mva_eligible"`
}

func (h *AccountingHandler) HandleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	accountID := chi.URLParam(r, "accountID")
	if accountID == "" {
		Error(w, http.StatusBadRequest, "account ID is required")
		return
	}

	var req updateAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		Error(w, http.StatusBadRequest, "name is required")
		return
	}

	if err := h.svc.UpdateAccount(r.Context(), accountID, req.Name, req.Description, req.MVAEligible); err != nil {
		h.log.Error().Err(err).Msg("failed to update account")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.account_updated", "account", accountID,
			map[string]any{"name": req.Name, "mva_eligible": req.MVAEligible})
	}

	JSON(w, http.StatusOK, map[string]string{"message": "account updated"})
}

func (h *AccountingHandler) HandleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	accountID := chi.URLParam(r, "accountID")
	if accountID == "" {
		Error(w, http.StatusBadRequest, "account ID is required")
		return
	}

	if err := h.svc.DeactivateAccount(r.Context(), accountID); err != nil {
		h.log.Error().Err(err).Msg("failed to deactivate account")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.account_deactivated", "account", accountID, nil)
	}

	JSON(w, http.StatusOK, map[string]string{"message": "account deactivated"})
}
