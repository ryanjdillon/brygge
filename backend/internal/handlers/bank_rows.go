package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/middleware"
)

// BankRowsHandler backs the new Tildel-tab endpoints for DIL-392.
// Service-side reads/writes live in accounting/bank_row_*.go; this
// handler validates input and wires audit.
type BankRowsHandler struct {
	svc   *accounting.Service
	audit *audit.Service
	log   zerolog.Logger
}

func NewBankRowsHandler(svc *accounting.Service, auditService *audit.Service, log zerolog.Logger) *BankRowsHandler {
	return &BankRowsHandler{
		svc:   svc,
		audit: auditService,
		log:   log.With().Str("handler", "bank_rows").Logger(),
	}
}

func (h *BankRowsHandler) HandleListUnmatched(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	year, _ := strconv.Atoi(q.Get("year"))
	kind := q.Get("kind")
	rows, err := h.svc.ListUnmatchedBankRows(ctx, claims.ClubID, kind, q.Get("q"), year, limit, offset)
	if err != nil {
		h.log.Error().Err(err).Msg("list unmatched bank rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *BankRowsHandler) HandleCountUnmatched(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	year, _ := strconv.Atoi(r.URL.Query().Get("year"))
	n, err := h.svc.CountUnmatchedBankRows(ctx, claims.ClubID, year)
	if err != nil {
		h.log.Error().Err(err).Msg("count unmatched bank rows")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"count": n})
}

func (h *BankRowsHandler) HandleCountUnmatchedByYear(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	counts, err := h.svc.CountUnmatchedBankRowsByYear(ctx, claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("count unmatched bank rows by year")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"by_year": counts})
}

func (h *BankRowsHandler) HandleSuggestions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rowID := chi.URLParam(r, "rowID")
	if rowID == "" {
		Error(w, http.StatusBadRequest, "rowID required")
		return
	}
	res, err := h.svc.BankRowSuggestionsFor(ctx, claims.ClubID, rowID)
	if err != nil {
		h.log.Warn().Err(err).Str("row_id", rowID).Msg("bank row suggestions")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	JSON(w, http.StatusOK, res)
}

func (h *BankRowsHandler) HandlePotentialInvoices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rowID := chi.URLParam(r, "rowID")
	if rowID == "" {
		Error(w, http.StatusBadRequest, "rowID required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	res, err := h.svc.PotentialInvoicesForRow(ctx, claims.ClubID, rowID, r.URL.Query().Get("q"), limit)
	if err != nil {
		h.log.Warn().Err(err).Str("row_id", rowID).Msg("potential invoices")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	JSON(w, http.StatusOK, map[string]any{"items": res, "potential": true})
}

type assignInvoiceReq struct {
	InvoiceID string `json:"invoice_id"`
}

func (h *BankRowsHandler) HandleAssignInvoice(w http.ResponseWriter, r *http.Request) {
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
	journalID, err := h.svc.AssignBankRowToInvoice(ctx, claims.ClubID, claims.UserID, rowID, req.InvoiceID)
	if err != nil {
		h.log.Warn().Err(err).Str("row_id", rowID).Msg("assign invoice")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionBankRowAssignedInvoice, "bank_import_row", rowID,
			map[string]any{
				"invoice_id":       req.InvoiceID,
				"journal_entry_id": journalID,
			})
	}
	JSON(w, http.StatusOK, map[string]any{"journal_entry_id": journalID})
}

type assignInvoiceMultiReq struct {
	RowIDs    []string `json:"row_ids"`
	InvoiceID string   `json:"invoice_id"`
}

func (h *BankRowsHandler) HandleAssignInvoiceMulti(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req assignInvoiceMultiReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.InvoiceID == "" || len(req.RowIDs) < 2 {
		Error(w, http.StatusBadRequest, "invoice_id and at least two row_ids required")
		return
	}
	journalIDs, err := h.svc.AssignMultipleBankRowsToInvoice(ctx, claims.ClubID, claims.UserID, req.RowIDs, req.InvoiceID)
	if err != nil {
		h.log.Warn().Err(err).Strs("row_ids", req.RowIDs).Msg("assign invoice multi")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionBankRowAssignedInvoice, "bank_import_rows", strings.Join(req.RowIDs, ","),
			map[string]any{
				"invoice_id":        req.InvoiceID,
				"row_ids":           req.RowIDs,
				"journal_entry_ids": journalIDs,
			})
	}
	JSON(w, http.StatusOK, map[string]any{"journal_entry_ids": journalIDs})
}

type assignAccountReq struct {
	AccountCode string `json:"account_code"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
}

func (h *BankRowsHandler) HandleAssignAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rowID := chi.URLParam(r, "rowID")
	var req assignAccountReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AccountCode == "" || req.Kind == "" {
		Error(w, http.StatusBadRequest, "account_code and kind required")
		return
	}
	journalID, err := h.svc.AssignBankRowToAccount(ctx, claims.ClubID, claims.UserID, rowID, req.AccountCode, req.Kind, req.Description)
	if err != nil {
		h.log.Warn().Err(err).Str("row_id", rowID).Msg("assign account")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionBankRowAssignedAccount, "bank_import_row", rowID,
			map[string]any{
				"account_code":     req.AccountCode,
				"kind":             req.Kind,
				"description":      req.Description,
				"journal_entry_id": journalID,
			})
	}
	JSON(w, http.StatusOK, map[string]any{"journal_entry_id": journalID})
}

type dismissReq struct {
	Reason string `json:"reason"`
}

func (h *BankRowsHandler) HandleDismiss(w http.ResponseWriter, r *http.Request) {
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
	if err := h.svc.DismissBankRow(ctx, claims.ClubID, claims.UserID, rowID, req.Reason); err != nil {
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionBankRowDismissed, "bank_import_row", rowID,
			map[string]any{"reason": req.Reason})
	}
	JSON(w, http.StatusOK, map[string]string{"status": "dismissed"})
}

type unassignReq struct {
	Confirm bool `json:"confirm"`
}

func (h *BankRowsHandler) HandleUnassign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rowID := chi.URLParam(r, "rowID")
	var req unassignReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.Confirm {
		Error(w, http.StatusBadRequest, "confirm: true required to unassign")
		return
	}
	priorJournal, priorInvoice, err := h.svc.UnassignBankRow(ctx, claims.ClubID, claims.UserID, rowID)
	if err != nil {
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionBankRowUnassigned, "bank_import_row", rowID,
			map[string]any{
				"prior_journal_entry_id": priorJournal,
				"prior_invoice_id":       priorInvoice,
			})
	}
	JSON(w, http.StatusOK, map[string]string{"status": "unassigned"})
}
