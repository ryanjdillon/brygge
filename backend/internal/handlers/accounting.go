package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// ── Bank Import ─────────────────────────────────────────────

func (h *AccountingHandler) HandleListBankFormats(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, accounting.ListBankFormats())
}

func (h *AccountingHandler) HandleImportBankStatement(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		Error(w, http.StatusBadRequest, "file too large or invalid multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		Error(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	formatName := r.FormValue("format")
	periodID := r.FormValue("period_id")
	if formatName == "" || periodID == "" {
		Error(w, http.StatusBadRequest, "format and period_id are required")
		return
	}

	bankFormat, ok := accounting.BankFormats[formatName]
	if !ok {
		Error(w, http.StatusBadRequest, "unknown bank format: "+formatName)
		return
	}

	parser := &accounting.CSVParser{Format: bankFormat}
	rows, err := parser.Parse(file)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to parse CSV")
		Error(w, http.StatusBadRequest, "failed to parse CSV: "+err.Error())
		return
	}

	var importID string
	err = h.svc.DB().QueryRow(r.Context(),
		`INSERT INTO bank_imports (club_id, filename, format, imported_by, row_count)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		claims.ClubID, header.Filename, formatName, claims.UserID, len(rows),
	).Scan(&importID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create import record")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	matched, err := h.svc.ImportBankRows(r.Context(), claims.ClubID, importID, periodID, rows)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to import bank rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.svc.DB().Exec(r.Context(), `UPDATE bank_imports SET matched_count = $1 WHERE id = $2`, matched, importID)

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.bank_imported", "bank_import", importID,
			map[string]any{"filename": header.Filename, "format": formatName, "rows": len(rows), "matched": matched})
	}

	JSON(w, http.StatusCreated, map[string]any{
		"id": importID, "filename": header.Filename, "format": formatName,
		"rows": len(rows), "matched": matched,
	})
}

func (h *AccountingHandler) HandleGetBankImport(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	importID := chi.URLParam(r, "importID")
	dbRows, err := h.svc.DB().Query(r.Context(),
		`SELECT bir.id, bir.row_date::text, bir.description, bir.amount, bir.balance, bir.reference,
		        bir.kid_number, bir.counterpart, bir.journal_entry_id, bir.auto_matched
		 FROM bank_import_rows bir JOIN bank_imports bi ON bi.id = bir.bank_import_id
		 WHERE bi.id = $1 AND bi.club_id = $2 ORDER BY bir.row_date`,
		importID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get bank import rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer dbRows.Close()

	type importRow struct {
		ID             string   `json:"id"`
		Date           string   `json:"date"`
		Description    string   `json:"description"`
		Amount         float64  `json:"amount"`
		Balance        *float64 `json:"balance"`
		Reference      string   `json:"reference"`
		KID            string   `json:"kid_number"`
		Counterpart    string   `json:"counterpart"`
		JournalEntryID *string  `json:"journal_entry_id"`
		AutoMatched    bool     `json:"auto_matched"`
	}
	var importRows []importRow
	for dbRows.Next() {
		var row importRow
		if err := dbRows.Scan(&row.ID, &row.Date, &row.Description, &row.Amount, &row.Balance,
			&row.Reference, &row.KID, &row.Counterpart, &row.JournalEntryID, &row.AutoMatched); err != nil {
			continue
		}
		importRows = append(importRows, row)
	}
	if importRows == nil {
		importRows = []importRow{}
	}
	JSON(w, http.StatusOK, importRows)
}

func (h *AccountingHandler) HandleListUnmatchedRows(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	importID := chi.URLParam(r, "importID")
	dbRows, err := h.svc.DB().Query(r.Context(),
		`SELECT bir.id, bir.row_date::text, bir.description, bir.amount, bir.balance, bir.reference,
		        bir.kid_number, bir.counterpart
		 FROM bank_import_rows bir JOIN bank_imports bi ON bi.id = bir.bank_import_id
		 WHERE bi.id = $1 AND bi.club_id = $2 AND bir.journal_entry_id IS NULL
		 ORDER BY bir.row_date`,
		importID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get unmatched rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer dbRows.Close()

	type unmatchedRow struct {
		ID          string   `json:"id"`
		Date        string   `json:"date"`
		Description string   `json:"description"`
		Amount      float64  `json:"amount"`
		Balance     *float64 `json:"balance"`
		Reference   string   `json:"reference"`
		KID         string   `json:"kid_number"`
		Counterpart string   `json:"counterpart"`
	}
	var result []unmatchedRow
	for dbRows.Next() {
		var row unmatchedRow
		if err := dbRows.Scan(&row.ID, &row.Date, &row.Description, &row.Amount, &row.Balance,
			&row.Reference, &row.KID, &row.Counterpart); err != nil {
			continue
		}
		result = append(result, row)
	}
	if result == nil {
		result = []unmatchedRow{}
	}
	JSON(w, http.StatusOK, result)
}

type matchRowRequest struct {
	PeriodID          string  `json:"period_id"`
	DebitAccountCode  string  `json:"debit_account_code"`
	CreditAccountCode string  `json:"credit_account_code"`
	MVAAmount         float64 `json:"mva_amount"`
}

func (h *AccountingHandler) HandleMatchBankRow(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rowID := chi.URLParam(r, "rowID")
	var req matchRowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.PeriodID == "" || req.DebitAccountCode == "" || req.CreditAccountCode == "" {
		Error(w, http.StatusBadRequest, "period_id, debit_account_code, and credit_account_code are required")
		return
	}

	err := h.svc.MatchBankRow(r.Context(), claims.ClubID, req.PeriodID, rowID,
		req.DebitAccountCode, req.CreditAccountCode, claims.UserID, req.MVAAmount)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to match bank row")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.bank_row_matched", "bank_import_row", rowID,
			map[string]any{"debit": req.DebitAccountCode, "credit": req.CreditAccountCode})
	}

	JSON(w, http.StatusOK, map[string]string{"message": "row matched"})
}

// ── Mapping Rules ───────────────────────────────────────────

func (h *AccountingHandler) HandleListRules(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rules, err := h.svc.ListRules(r.Context(), claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list rules")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if rules == nil {
		rules = []accounting.MappingRule{}
	}
	JSON(w, http.StatusOK, rules)
}

type createRuleRequest struct {
	Name              string                    `json:"name"`
	Priority          int                       `json:"priority"`
	MatchField        string                    `json:"match_field"`
	MatchValue        string                    `json:"match_value"`
	MatchOperator     string                    `json:"match_operator"`
	DebitAccountCode  string                    `json:"debit_account_code"`
	CreditAccountCode string                    `json:"credit_account_code"`
	MVAEligible       accounting.MVAEligibility `json:"mva_eligible"`
}

func (h *AccountingHandler) HandleCreateRule(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req createRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.MatchField == "" || req.MatchValue == "" {
		Error(w, http.StatusBadRequest, "name, match_field, and match_value are required")
		return
	}

	id, err := h.svc.CreateRule(r.Context(), claims.ClubID, accounting.CreateRuleInput{
		Name:              req.Name,
		Priority:          req.Priority,
		MatchField:        req.MatchField,
		MatchValue:        req.MatchValue,
		MatchOperator:     req.MatchOperator,
		DebitAccountCode:  req.DebitAccountCode,
		CreditAccountCode: req.CreditAccountCode,
		MVAEligible:       req.MVAEligible,
	})
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create rule")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.rule_created", "mapping_rule", id,
			map[string]any{"name": req.Name, "field": req.MatchField, "value": req.MatchValue})
	}

	JSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (h *AccountingHandler) HandleUpdateRule(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	ruleID := chi.URLParam(r, "ruleID")
	var req createRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	err := h.svc.UpdateRule(r.Context(), ruleID, accounting.CreateRuleInput{
		Name:              req.Name,
		Priority:          req.Priority,
		MatchField:        req.MatchField,
		MatchValue:        req.MatchValue,
		MatchOperator:     req.MatchOperator,
		DebitAccountCode:  req.DebitAccountCode,
		CreditAccountCode: req.CreditAccountCode,
		MVAEligible:       req.MVAEligible,
	}, claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to update rule")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}

	JSON(w, http.StatusOK, map[string]string{"message": "rule updated"})
}

func (h *AccountingHandler) HandleDeleteRule(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	ruleID := chi.URLParam(r, "ruleID")
	if err := h.svc.DeleteRule(r.Context(), ruleID); err != nil {
		h.log.Error().Err(err).Msg("failed to delete rule")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.rule_deleted", "mapping_rule", ruleID, nil)
	}

	JSON(w, http.StatusOK, map[string]string{"message": "rule deleted"})
}

type autoMatchRequest struct {
	PeriodID string `json:"period_id"`
}

func (h *AccountingHandler) HandleAutoMatchImport(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	importID := chi.URLParam(r, "importID")
	var req autoMatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.PeriodID == "" {
		Error(w, http.StatusBadRequest, "period_id is required")
		return
	}

	matched, err := h.svc.AutoMatchImport(r.Context(), claims.ClubID, importID, req.PeriodID, claims.UserID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to auto-match import")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.auto_matched", "bank_import", importID,
			map[string]any{"matched": matched})
	}

	JSON(w, http.StatusOK, map[string]any{"matched": matched})
}

// ── Reports ─────────────────────────────────────────────────

func (h *AccountingHandler) HandleIncomeStatement(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	periodID := r.URL.Query().Get("period_id")
	if periodID == "" {
		Error(w, http.StatusBadRequest, "period_id is required")
		return
	}

	stmt, err := h.svc.IncomeStatement(r.Context(), claims.ClubID, periodID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate income statement")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, stmt)
}

func (h *AccountingHandler) HandleIncomeStatementPDF(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	periodID := r.URL.Query().Get("period_id")
	if periodID == "" {
		Error(w, http.StatusBadRequest, "period_id is required")
		return
	}

	stmt, err := h.svc.IncomeStatement(r.Context(), claims.ClubID, periodID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate income statement")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	header := h.getReportHeader(r.Context(), claims.ClubID, stmt.Year)
	pdfData, err := accounting.IncomeStatementPDF(header, stmt)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate PDF")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="resultatregnskap-%d.pdf"`, stmt.Year))
	w.Write(pdfData)
}

func (h *AccountingHandler) HandleBalanceSheet(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	periodID := r.URL.Query().Get("period_id")
	if periodID == "" {
		Error(w, http.StatusBadRequest, "period_id is required")
		return
	}

	bs, err := h.svc.BalanceSheet(r.Context(), claims.ClubID, periodID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate balance sheet")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, bs)
}

func (h *AccountingHandler) HandleBalanceSheetPDF(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	periodID := r.URL.Query().Get("period_id")
	if periodID == "" {
		Error(w, http.StatusBadRequest, "period_id is required")
		return
	}

	bs, err := h.svc.BalanceSheet(r.Context(), claims.ClubID, periodID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate balance sheet")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	header := h.getReportHeader(r.Context(), claims.ClubID, bs.Year)
	pdfData, err := accounting.BalanceSheetPDF(header, bs)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate PDF")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="balanse-%d.pdf"`, bs.Year))
	w.Write(pdfData)
}

func (h *AccountingHandler) HandleTrialBalance(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	periodID := r.URL.Query().Get("period_id")
	if periodID == "" {
		Error(w, http.StatusBadRequest, "period_id is required")
		return
	}

	tb, err := h.svc.TrialBalance(r.Context(), claims.ClubID, periodID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate trial balance")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, tb)
}

func (h *AccountingHandler) HandleGeneralLedger(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	periodID := r.URL.Query().Get("period_id")
	accountID := r.URL.Query().Get("account_id")
	if periodID == "" || accountID == "" {
		Error(w, http.StatusBadRequest, "period_id and account_id are required")
		return
	}

	gl, err := h.svc.GeneralLedger(r.Context(), claims.ClubID, periodID, accountID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate general ledger")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, gl)
}

func (h *AccountingHandler) getReportHeader(ctx context.Context, clubID string, year int) accounting.ReportHeader {
	header := accounting.ReportHeader{Year: year}
	if h.svc.DB() != nil {
		h.svc.DB().QueryRow(ctx,
			`SELECT name, COALESCE(org_number, '') FROM clubs WHERE id = $1`,
			clubID,
		).Scan(&header.ClubName, &header.OrgNumber)
	}
	return header
}

// ── Momskompensasjon ────────────────────────────────────────

func (h *AccountingHandler) HandleMomskompensasjon(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	periodID := r.URL.Query().Get("period_id")
	model := r.URL.Query().Get("model")
	if periodID == "" {
		Error(w, http.StatusBadRequest, "period_id is required")
		return
	}
	if model == "" {
		model = "simplified"
	}

	report, err := h.svc.Momskompensasjon(r.Context(), claims.ClubID, periodID, model)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to calculate momskompensasjon")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, report)
}

func (h *AccountingHandler) HandleMomskompensasjonPDF(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	periodID := r.URL.Query().Get("period_id")
	model := r.URL.Query().Get("model")
	if periodID == "" {
		Error(w, http.StatusBadRequest, "period_id is required")
		return
	}
	if model == "" {
		model = "simplified"
	}

	report, err := h.svc.Momskompensasjon(r.Context(), claims.ClubID, periodID, model)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to calculate momskompensasjon")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	header := h.getReportHeader(r.Context(), claims.ClubID, report.Year)
	pdfData, err := accounting.MomskompPDF(header, report)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to generate momskomp PDF")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.momskomp_pdf_generated", "momskomp", periodID,
			map[string]any{"model": model, "compensation": report.CompensationAmount})
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="momskompensasjon-%d-%s.pdf"`, report.Year, model))
	w.Write(pdfData)
}

type saveMomskompRequest struct {
	PeriodID string `json:"period_id"`
	Model    string `json:"model"`
}

func (h *AccountingHandler) HandleSaveMomskompReport(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req saveMomskompRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.PeriodID == "" {
		Error(w, http.StatusBadRequest, "period_id is required")
		return
	}
	if req.Model == "" {
		req.Model = "simplified"
	}

	report, err := h.svc.Momskompensasjon(r.Context(), claims.ClubID, req.PeriodID, req.Model)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to calculate momskompensasjon")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	id, err := h.svc.SaveMomskompReport(r.Context(), claims.ClubID, req.PeriodID, req.Model, claims.UserID, report)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to save momskomp report")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.momskomp_saved", "momskomp", id,
			map[string]any{"model": req.Model, "compensation": report.CompensationAmount})
	}

	JSON(w, http.StatusCreated, map[string]any{"id": id, "compensation_amount": report.CompensationAmount})
}

type updateMomskompStatusRequest struct {
	Status string `json:"status"`
}

func (h *AccountingHandler) HandleUpdateMomskompStatus(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	reportID := chi.URLParam(r, "reportID")
	var req updateMomskompStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Status != "submitted" && req.Status != "draft" {
		Error(w, http.StatusBadRequest, "status must be 'draft' or 'submitted'")
		return
	}

	if err := h.svc.UpdateMomskompStatus(r.Context(), reportID, req.Status); err != nil {
		h.log.Error().Err(err).Msg("failed to update momskomp status")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.momskomp_status_updated", "momskomp", reportID,
			map[string]any{"status": req.Status})
	}

	JSON(w, http.StatusOK, map[string]string{"message": "status updated"})
}
