package handlers

import (
	"encoding/json"
	"errors"
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

// ── Fiscal Periods ──────────────────────────────────────────

func (h *AccountingHandler) HandleListPeriods(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	periods, err := h.svc.ListPeriods(r.Context(), claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list periods")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if periods == nil {
		periods = []accounting.FiscalPeriod{}
	}
	JSON(w, http.StatusOK, periods)
}

type createPeriodRequest struct {
	Year int `json:"year"`
}

func (h *AccountingHandler) HandleCreatePeriod(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req createPeriodRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Year < 2000 || req.Year > 2100 {
		Error(w, http.StatusBadRequest, "year must be between 2000 and 2100")
		return
	}

	period, err := h.svc.CreatePeriod(r.Context(), claims.ClubID, req.Year)
	if err != nil {
		h.log.Error().Err(err).Int("year", req.Year).Msg("failed to create period")
		Error(w, http.StatusConflict, "could not create period (may already exist)")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.period_created", "fiscal_period", period.ID,
			map[string]any{"year": req.Year})
	}

	JSON(w, http.StatusCreated, period)
}

func (h *AccountingHandler) HandleClosePeriod(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	periodID := chi.URLParam(r, "periodID")
	if err := h.svc.ClosePeriod(r.Context(), periodID, claims.UserID); err != nil {
		h.log.Error().Err(err).Msg("failed to close period")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.period_closed", "fiscal_period", periodID, nil)
	}

	JSON(w, http.StatusOK, map[string]string{"message": "period closed"})
}

func (h *AccountingHandler) HandleReopenPeriod(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	periodID := chi.URLParam(r, "periodID")
	if err := h.svc.ReopenPeriod(r.Context(), periodID); err != nil {
		h.log.Error().Err(err).Msg("failed to reopen period")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.period_reopened", "fiscal_period", periodID, nil)
	}

	JSON(w, http.StatusOK, map[string]string{"message": "period reopened"})
}

// ── Journal Entries ─────────────────────────────────────────

func (h *AccountingHandler) HandleListJournalEntries(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	filters := accounting.JournalFilters{
		PeriodID:  r.URL.Query().Get("period_id"),
		Status:    r.URL.Query().Get("status"),
		StartDate: r.URL.Query().Get("start_date"),
		EndDate:   r.URL.Query().Get("end_date"),
	}

	entries, err := h.svc.ListJournalEntries(r.Context(), claims.ClubID, filters)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list journal entries")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if entries == nil {
		entries = []accounting.JournalEntry{}
	}
	JSON(w, http.StatusOK, entries)
}

type createJournalEntryRequest struct {
	FiscalPeriodID string                    `json:"fiscal_period_id"`
	EntryDate      string                    `json:"entry_date"`
	Description    string                    `json:"description"`
	AttachmentURL  *string                   `json:"attachment_url"`
	Lines          []createJournalLineRequest `json:"lines"`
}

type createJournalLineRequest struct {
	AccountCode string  `json:"account_code"`
	Debit       float64 `json:"debit"`
	Credit      float64 `json:"credit"`
	Description string  `json:"description"`
	MVAAmount   float64 `json:"mva_amount"`
}

func (h *AccountingHandler) HandleCreateJournalEntry(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req createJournalEntryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.FiscalPeriodID == "" || req.EntryDate == "" || req.Description == "" || len(req.Lines) == 0 {
		Error(w, http.StatusBadRequest, "fiscal_period_id, entry_date, description, and at least one line are required")
		return
	}

	lines := make([]accounting.CreateJournalLineInput, len(req.Lines))
	for i, l := range req.Lines {
		if l.AccountCode == "" {
			Error(w, http.StatusBadRequest, "account_code is required on each line")
			return
		}
		lines[i] = accounting.CreateJournalLineInput{
			AccountCode: l.AccountCode,
			Debit:       l.Debit,
			Credit:      l.Credit,
			Description: l.Description,
			MVAAmount:   l.MVAAmount,
		}
	}

	entry, err := h.svc.CreateJournalEntry(r.Context(), accounting.CreateJournalEntryInput{
		FiscalPeriodID: req.FiscalPeriodID,
		EntryDate:      req.EntryDate,
		Description:    req.Description,
		AttachmentURL:  req.AttachmentURL,
		CreatedBy:      claims.UserID,
		ClubID:         claims.ClubID,
		Lines:          lines,
	})
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create journal entry")
		if err == accounting.ErrPeriodClosed {
			Error(w, http.StatusConflict, err.Error())
		} else {
			Error(w, http.StatusBadRequest, err.Error())
		}
		return
	}

	JSON(w, http.StatusCreated, entry)
}

func (h *AccountingHandler) HandleGetJournalEntry(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	entryID := chi.URLParam(r, "entryID")
	entry, err := h.svc.GetJournalEntry(r.Context(), entryID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get journal entry")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if entry == nil {
		Error(w, http.StatusNotFound, "journal entry not found")
		return
	}

	JSON(w, http.StatusOK, entry)
}

func (h *AccountingHandler) HandlePostJournalEntry(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	entryID := chi.URLParam(r, "entryID")
	err := h.svc.PostJournalEntry(r.Context(), entryID, claims.UserID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to post journal entry")
		switch {
		case errors.Is(err, accounting.ErrUnbalancedEntry):
			Error(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, accounting.ErrPeriodClosed):
			Error(w, http.StatusConflict, err.Error())
		case errors.Is(err, accounting.ErrEntryNotDraft):
			Error(w, http.StatusBadRequest, err.Error())
		default:
			Error(w, http.StatusInternalServerError, "internal error")
		}
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.entry_posted", "journal_entry", entryID, nil)
	}

	JSON(w, http.StatusOK, map[string]string{"message": "entry posted"})
}

func (h *AccountingHandler) HandleVoidJournalEntry(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	entryID := chi.URLParam(r, "entryID")
	reversal, err := h.svc.VoidJournalEntry(r.Context(), entryID, claims.UserID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to void journal entry")
		if errors.Is(err, accounting.ErrEntryNotPosted) {
			Error(w, http.StatusBadRequest, err.Error())
		} else {
			Error(w, http.StatusInternalServerError, "internal error")
		}
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.entry_voided", "journal_entry", entryID,
			map[string]any{"reversal_id": reversal.ID})
	}

	JSON(w, http.StatusOK, reversal)
}

// ── Sync ────────────────────────────────────────────────────

type syncRequest struct {
	PeriodID string `json:"period_id"`
}

func (h *AccountingHandler) HandleSyncPayments(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req syncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.PeriodID == "" {
		Error(w, http.StatusBadRequest, "period_id is required")
		return
	}

	result, err := h.svc.SyncPayments(r.Context(), claims.ClubID, req.PeriodID, claims.UserID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to sync payments")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.payments_synced", "sync", req.PeriodID,
			map[string]any{"synced": result.Synced, "skipped": result.Skipped})
	}

	JSON(w, http.StatusOK, result)
}

func (h *AccountingHandler) HandleSyncInvoices(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req syncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.PeriodID == "" {
		Error(w, http.StatusBadRequest, "period_id is required")
		return
	}

	result, err := h.svc.SyncInvoices(r.Context(), claims.ClubID, req.PeriodID, claims.UserID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to sync invoices")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.invoices_synced", "sync", req.PeriodID,
			map[string]any{"synced": result.Synced, "skipped": result.Skipped})
	}

	JSON(w, http.StatusOK, result)
}
