package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
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
	// period_id is optional now — when blank each entry's period is
	// resolved from the row's date (auto-created as calendar year if
	// missing). A value here forces every row into that one period.
	periodID := r.FormValue("period_id")
	bankAccountCode := r.FormValue("bank_account_code")
	if formatName == "" {
		Error(w, http.StatusBadRequest, "format is required")
		return
	}
	if bankAccountCode == "" {
		bankAccountCode = "1920"
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

	// Auto-match the statement's own account number against the club's
	// registered bank accounts. When the user-selected GL code doesn't
	// match what the file actually contains, we trust the file —
	// uploading a høyrente statement with GL code 1920 (drift) was a
	// real incident this guards against. See DIL-342.
	inferred := accounting.InferOwnAccountNumber(rows)
	autoMatched := false
	if inferred != "" {
		var matchedGL string
		err := h.svc.DB().QueryRow(r.Context(),
			`SELECT gl_code FROM club_bank_accounts
			  WHERE club_id = $1
			    AND archived_at IS NULL
			    AND regexp_replace(account_number, '\D', '', 'g')
			      = regexp_replace($2, '\D', '', 'g')`,
			claims.ClubID, inferred,
		).Scan(&matchedGL)
		if err == nil && matchedGL != "" && matchedGL != bankAccountCode {
			h.log.Info().
				Str("user_selected", bankAccountCode).
				Str("matched", matchedGL).
				Str("account_number", inferred).
				Msg("bank_import: auto-matched statement to registered account")
			bankAccountCode = matchedGL
			autoMatched = true
		}
	}

	var importID string
	err = h.svc.DB().QueryRow(r.Context(),
		`INSERT INTO bank_imports (club_id, filename, format, bank_account_code, imported_by, row_count)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		claims.ClubID, header.Filename, formatName, bankAccountCode, claims.UserID, len(rows),
	).Scan(&importID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create import record")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	result, err := h.svc.ImportBankRows(r.Context(), claims.ClubID, importID, periodID, rows)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to import bank rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.svc.DB().Exec(r.Context(), `UPDATE bank_imports SET matched_count = $1, row_count = $2 WHERE id = $3`, result.Matched, result.Imported, importID)

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.bank_imported", "bank_import", importID,
			map[string]any{
				"filename":    header.Filename,
				"format":      formatName,
				"rows_total":  len(rows),
				"imported":    result.Imported,
				"skipped_dup": result.SkippedDup,
				"matched":     result.Matched,
				"transfers":   result.Transfers,
			})
	}

	JSON(w, http.StatusCreated, map[string]any{
		"id":                importID,
		"filename":          header.Filename,
		"format":            formatName,
		"rows_total":        len(rows),
		"imported":          result.Imported,
		"skipped_dup":       result.SkippedDup,
		"matched":           result.Matched,
		"transfers":         result.Transfers,
		"closed_periods":    result.ClosedPeriods,
		"bank_account_code": bankAccountCode,
		"auto_matched":      autoMatched,
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

func (h *AccountingHandler) HandleListBankRowsByAccount(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	q := r.URL.Query()
	accountCode := strings.TrimSpace(q.Get("account_code"))
	fromStr := strings.TrimSpace(q.Get("from"))
	toStr := strings.TrimSpace(q.Get("to"))
	if accountCode == "" || fromStr == "" || toStr == "" {
		Error(w, http.StatusBadRequest, "account_code, from, to required")
		return
	}

	dbRows, err := h.svc.DB().Query(r.Context(),
		`SELECT bir.id, bir.row_date::text, bir.description, bir.amount, bir.balance, bir.reference,
		        bir.kid_number, bir.counterpart, bir.journal_entry_id, bir.auto_matched
		 FROM bank_import_rows bir JOIN bank_imports bi ON bi.id = bir.bank_import_id
		 WHERE bi.club_id = $1 AND bi.bank_account_code = $2
		   AND bir.row_date >= $3::date AND bir.row_date <= $4::date
		 ORDER BY bir.row_date, bir.created_at`,
		claims.ClubID, accountCode, fromStr, toStr,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to query bank rows by account")
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
	importRows := []importRow{}
	for dbRows.Next() {
		var row importRow
		if err := dbRows.Scan(&row.ID, &row.Date, &row.Description, &row.Amount, &row.Balance,
			&row.Reference, &row.KID, &row.Counterpart, &row.JournalEntryID, &row.AutoMatched); err != nil {
			continue
		}
		importRows = append(importRows, row)
	}
	JSON(w, http.StatusOK, importRows)
}

func (h *AccountingHandler) HandleListVippsRowsByMSN(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	q := r.URL.Query()
	msn := strings.TrimSpace(q.Get("msn"))
	fromStr := strings.TrimSpace(q.Get("from"))
	toStr := strings.TrimSpace(q.Get("to"))
	if msn == "" || fromStr == "" || toStr == "" {
		Error(w, http.StatusBadRequest, "msn, from, to required")
		return
	}

	dbRows, err := h.svc.DB().Query(r.Context(),
		`SELECT vir.id, vir.row_type, vir.tx_at, vir.booking_date, vir.amount, vir.fee, vir.net_amount,
		        vir.customer_name, vir.customer_phone_masked, vir.message, vir.psp_ref, vir.order_id,
		        vir.settlement_number, vir.payout_account, vir.scheduled_payout_date, vir.journal_entry_id
		 FROM vipps_import_rows vir
		 WHERE vir.club_id = $1 AND vir.msn = $2
		   AND COALESCE(vir.booking_date, vir.tx_at::date, vir.scheduled_payout_date) >= $3::date
		   AND COALESCE(vir.booking_date, vir.tx_at::date, vir.scheduled_payout_date) <= $4::date
		 ORDER BY COALESCE(vir.booking_date, vir.tx_at::date, vir.scheduled_payout_date), vir.created_at`,
		claims.ClubID, msn, fromStr, toStr,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list vipps rows by msn")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer dbRows.Close()

	type item struct {
		ID                  string     `json:"id"`
		RowType             string     `json:"row_type"`
		TxAt                *time.Time `json:"tx_at"`
		BookingDate         *string    `json:"booking_date"`
		Amount              float64    `json:"amount"`
		Fee                 float64    `json:"fee"`
		NetAmount           float64    `json:"net_amount"`
		CustomerName        string     `json:"customer_name"`
		CustomerPhoneMasked string     `json:"customer_phone_masked"`
		Message             string     `json:"message"`
		PspRef              string     `json:"psp_ref"`
		OrderID             string     `json:"order_id"`
		SettlementNumber    string     `json:"settlement_number"`
		PayoutAccount       string     `json:"payout_account"`
		ScheduledPayoutDate *string    `json:"scheduled_payout_date"`
		JournalEntryID      *string    `json:"journal_entry_id"`
	}
	out := []item{}
	for dbRows.Next() {
		var it item
		if err := dbRows.Scan(&it.ID, &it.RowType, &it.TxAt, &it.BookingDate, &it.Amount, &it.Fee, &it.NetAmount,
			&it.CustomerName, &it.CustomerPhoneMasked, &it.Message, &it.PspRef, &it.OrderID,
			&it.SettlementNumber, &it.PayoutAccount, &it.ScheduledPayoutDate, &it.JournalEntryID); err == nil {
			out = append(out, it)
		}
	}
	JSON(w, http.StatusOK, out)
}

func (h *AccountingHandler) HandleListBankImports(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.svc.DB().Query(r.Context(),
		`SELECT id, filename, format, bank_account_code, row_count, matched_count, created_at::text
		 FROM bank_imports WHERE club_id = $1 ORDER BY created_at DESC LIMIT 100`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list bank imports")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type item struct {
		ID           string `json:"id"`
		Filename     string `json:"filename"`
		Format       string `json:"format"`
		AccountCode  string `json:"account_code"`
		RowCount     int    `json:"row_count"`
		MatchedCount int    `json:"matched_count"`
		CreatedAt    string `json:"created_at"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.Filename, &it.Format, &it.AccountCode, &it.RowCount, &it.MatchedCount, &it.CreatedAt); err == nil {
			out = append(out, it)
		}
	}
	JSON(w, http.StatusOK, out)
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

type reassignBankImportRequest struct {
	BankAccountCode string `json:"bank_account_code"`
}

// HandleReassignBankImport changes which GL bank account an existing
// import belongs to and cascades the change through every journal
// line that was matched against rows of this import. The bank-side
// line on each entry (the leg that referenced the old account) gets
// rewritten to point at the new account; counter-legs (revenue,
// expense, etc.) are untouched.
//
// Refuses only when the cascade would touch a journal entry whose
// fiscal period is closed or locked, or that has been voided — those
// are immutable accounting state. See DIL-343.
func (h *AccountingHandler) HandleReassignBankImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	importID := chi.URLParam(r, "importID")
	var req reassignBankImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.BankAccountCode == "" {
		Error(w, http.StatusBadRequest, "bank_account_code is required")
		return
	}

	var oldCode string
	if err := h.svc.DB().QueryRow(ctx,
		`SELECT bank_account_code FROM bank_imports WHERE id = $1 AND club_id = $2`,
		importID, claims.ClubID,
	).Scan(&oldCode); err != nil {
		Error(w, http.StatusNotFound, "bank import not found")
		return
	}
	if oldCode == req.BankAccountCode {
		JSON(w, http.StatusOK, map[string]any{"status": "unchanged"})
		return
	}

	tx, err := h.svc.DB().BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		h.log.Error().Err(err).Msg("begin tx")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	// Resolve both account ids (old + new) for this club.
	var oldAccountID, newAccountID string
	if err := tx.QueryRow(ctx,
		`SELECT id FROM accounts WHERE club_id = $1 AND code = $2`,
		claims.ClubID, oldCode,
	).Scan(&oldAccountID); err != nil {
		Error(w, http.StatusInternalServerError, "old account not found in chart of accounts")
		return
	}
	if err := tx.QueryRow(ctx,
		`SELECT id FROM accounts WHERE club_id = $1 AND code = $2`,
		claims.ClubID, req.BankAccountCode,
	).Scan(&newAccountID); err != nil {
		Error(w, http.StatusBadRequest, "target account not found in chart of accounts")
		return
	}

	// Refuse if any affected journal entry is in a closed/locked period
	// or has been voided — those are accounting facts we can't rewrite.
	var blockedPeriods []string
	rows, err := tx.Query(ctx,
		`SELECT DISTINCT fp.year::text
		   FROM bank_import_rows bir
		   JOIN journal_entries je ON je.id = bir.journal_entry_id
		   JOIN fiscal_periods fp ON fp.id = je.fiscal_period_id
		  WHERE bir.bank_import_id = $1
		    AND (fp.status IN ('closed','locked') OR je.status = 'voided')`,
		importID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("scan closed periods")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err == nil {
			blockedPeriods = append(blockedPeriods, label)
		}
	}
	rows.Close()
	if len(blockedPeriods) > 0 {
		Error(w, http.StatusConflict,
			"cannot reassign: some matched rows live in closed/locked or voided journals ("+strings.Join(blockedPeriods, ", ")+") — reopen them first or void the entries manually")
		return
	}

	// Cascade: rewrite the bank-side line of every affected journal
	// entry from the old account to the new account. There is exactly
	// one such line per entry (created by MatchBankRow), so this is a
	// straight account_id swap, not a re-balance.
	cascadeTag, err := tx.Exec(ctx,
		`UPDATE journal_lines
		    SET account_id = $1
		  WHERE account_id = $2
		    AND journal_entry_id IN (
		      SELECT bir.journal_entry_id
		        FROM bank_import_rows bir
		       WHERE bir.bank_import_id = $3
		         AND bir.journal_entry_id IS NOT NULL
		    )`,
		newAccountID, oldAccountID, importID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("cascade journal lines")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	cascaded := cascadeTag.RowsAffected()

	if _, err := tx.Exec(ctx,
		`UPDATE bank_imports SET bank_account_code = $1 WHERE id = $2 AND club_id = $3`,
		req.BankAccountCode, importID, claims.ClubID,
	); err != nil {
		h.log.Error().Err(err).Msg("reassign bank import")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Msg("commit reassign")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.bank_import_reassigned", "bank_import", importID,
			map[string]any{
				"from":              oldCode,
				"to":                req.BankAccountCode,
				"cascaded_journals": cascaded,
			})
	}
	JSON(w, http.StatusOK, map[string]any{
		"status":            "reassigned",
		"from":              oldCode,
		"to":                req.BankAccountCode,
		"cascaded_journals": cascaded,
	})
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

// ── Rebuild invoice bilags ──────────────────────────────────

func (h *AccountingHandler) HandleRebuildInvoiceBilags(w http.ResponseWriter, r *http.Request) {
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

	result, deleted, err := h.svc.RebuildInvoiceBilags(r.Context(), claims.ClubID, req.PeriodID, claims.UserID)
	if err != nil {
		Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.invoice_bilags_rebuilt", "fiscal_period", req.PeriodID,
			map[string]any{"deleted": deleted, "resynced": result.Synced, "skipped": result.Skipped})
	}

	JSON(w, http.StatusOK, map[string]any{
		"deleted":  deleted,
		"resynced": result.Synced,
		"skipped":  result.Skipped,
	})
}

// ── Full bank sync ──────────────────────────────────────────

func (h *AccountingHandler) HandleBankSync(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	result, err := h.svc.BankSync(r.Context(), claims.ClubID, claims.UserID)
	if err != nil {
		h.log.Error().Err(err).Msg("bank sync failed")
		Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.bank_synced", "bank", "",
			map[string]any{
				"kid_matched":      result.KIDMatched,
				"vipps_reconciled": result.VippsReconciled,
				"transfers_linked": result.TransfersLinked,
				"closed_periods":   result.ClosedPeriods,
			})
	}
	JSON(w, http.StatusOK, result)
}

// ── Vipps Reconciliation ────────────────────────────────────

func (h *AccountingHandler) HandleVippsReconcilePreview(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rowID := chi.URLParam(r, "rowID")
	preview, err := h.svc.ReconcileVippsPreview(r.Context(), claims.ClubID, rowID)
	if err != nil {
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	JSON(w, http.StatusOK, preview)
}

type vippsReconcileConfirmRequest struct {
	// PeriodID is optional. When blank the period is auto-resolved from
	// the bank row's date.
	PeriodID string                          `json:"period_id"`
	Lines    []accounting.VippsReconcileLine `json:"lines"`
}

func (h *AccountingHandler) HandleVippsReconcileConfirm(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rowID := chi.URLParam(r, "rowID")

	var req vippsReconcileConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	entryID, err := h.svc.ReconcileVippsConfirm(r.Context(), claims.ClubID, rowID, req.PeriodID, claims.UserID, req.Lines)
	if err != nil {
		Error(w, http.StatusBadRequest, err.Error())
		return
	}

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.vipps_reconciled", "bank_import_row", rowID,
			map[string]any{"journal_entry_id": entryID, "lines": len(req.Lines)})
	}

	JSON(w, http.StatusCreated, map[string]any{"journal_entry_id": entryID})
}

// ── Vipps Import ────────────────────────────────────────────

func (h *AccountingHandler) HandleImportVippsSettlement(w http.ResponseWriter, r *http.Request) {
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

	rows, err := accounting.ParseVippsCSV(file)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to parse Vipps CSV")
		Error(w, http.StatusBadRequest, "failed to parse CSV: "+err.Error())
		return
	}

	msn := ""
	for _, row := range rows {
		if row.MSN != "" {
			msn = row.MSN
			break
		}
	}

	var importID string
	err = h.svc.DB().QueryRow(r.Context(),
		`INSERT INTO vipps_imports (club_id, filename, msn, imported_by, row_count)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		claims.ClubID, header.Filename, msn, claims.UserID, len(rows),
	).Scan(&importID)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to create vipps_imports row")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	result, err := h.svc.ImportVippsRows(r.Context(), claims.ClubID, importID, rows)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to insert vipps rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.svc.DB().Exec(r.Context(),
		`UPDATE vipps_imports SET row_count = $1 WHERE id = $2`,
		result.Imported, importID)

	if h.audit != nil {
		h.audit.LogAction(r.Context(), claims.ClubID, claims.UserID, r.RemoteAddr,
			"accounting.vipps_imported", "vipps_import", importID,
			map[string]any{
				"filename":    header.Filename,
				"msn":         msn,
				"rows_total":  len(rows),
				"imported":    result.Imported,
				"skipped_dup": result.SkippedDup,
			})
	}

	JSON(w, http.StatusCreated, map[string]any{
		"id":          importID,
		"filename":    header.Filename,
		"msn":         msn,
		"rows_total":  len(rows),
		"imported":    result.Imported,
		"skipped_dup": result.SkippedDup,
	})
}

func (h *AccountingHandler) HandleListVippsImports(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	rows, err := h.svc.DB().Query(r.Context(),
		`SELECT id, filename, msn, row_count, created_at::text
		 FROM vipps_imports WHERE club_id = $1 ORDER BY created_at DESC LIMIT 100`,
		claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list vipps imports")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type item struct {
		ID        string `json:"id"`
		Filename  string `json:"filename"`
		MSN       string `json:"msn"`
		RowCount  int    `json:"row_count"`
		CreatedAt string `json:"created_at"`
	}
	out := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.ID, &it.Filename, &it.MSN, &it.RowCount, &it.CreatedAt); err == nil {
			out = append(out, it)
		}
	}
	JSON(w, http.StatusOK, out)
}

func (h *AccountingHandler) HandleGetVippsImport(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}

	importID := chi.URLParam(r, "importID")
	rows, err := h.svc.DB().Query(r.Context(),
		`SELECT vir.id, vir.row_type, vir.tx_at, vir.booking_date, vir.amount, vir.fee, vir.net_amount,
		        vir.customer_name, vir.customer_phone_masked, vir.message, vir.psp_ref, vir.order_id,
		        vir.settlement_number, vir.payout_account, vir.scheduled_payout_date, vir.journal_entry_id
		 FROM vipps_import_rows vir
		 JOIN vipps_imports vi ON vi.id = vir.vipps_import_id
		 WHERE vi.id = $1 AND vi.club_id = $2
		 ORDER BY vir.tx_at NULLS LAST, vir.id`,
		importID, claims.ClubID,
	)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get vipps import")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	type row struct {
		ID                  string  `json:"id"`
		RowType             string  `json:"row_type"`
		TxAt                *string `json:"tx_at"`
		BookingDate         *string `json:"booking_date"`
		Amount              float64 `json:"amount"`
		Fee                 float64 `json:"fee"`
		NetAmount           float64 `json:"net_amount"`
		CustomerName        string  `json:"customer_name"`
		CustomerPhoneMasked string  `json:"customer_phone_masked"`
		Message             string  `json:"message"`
		PSPRef              string  `json:"psp_ref"`
		OrderID             string  `json:"order_id"`
		SettlementNumber    string  `json:"settlement_number"`
		PayoutAccount       string  `json:"payout_account"`
		ScheduledPayoutDate *string `json:"scheduled_payout_date"`
		JournalEntryID      *string `json:"journal_entry_id"`
	}

	out := []row{}
	for rows.Next() {
		var (
			r                            row
			txAt, bookingDate, scheduled *string
		)
		if err := rows.Scan(&r.ID, &r.RowType, &txAt, &bookingDate,
			&r.Amount, &r.Fee, &r.NetAmount,
			&r.CustomerName, &r.CustomerPhoneMasked, &r.Message,
			&r.PSPRef, &r.OrderID, &r.SettlementNumber, &r.PayoutAccount,
			&scheduled, &r.JournalEntryID); err != nil {
			continue
		}
		r.TxAt = txAt
		r.BookingDate = bookingDate
		r.ScheduledPayoutDate = scheduled
		out = append(out, r)
	}
	JSON(w, http.StatusOK, out)
}

