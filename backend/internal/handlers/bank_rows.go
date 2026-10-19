package handlers

import (
	"encoding/json"
	"errors"
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

// HandleAutoAssignPreview projects what the bank auto-assign rule (BRY-366)
// would book now, as if the club had opted in, and reports the real
// setting. Read-only; the items carry row id, date, amount and faktura
// number, never a payer name or description.
func (h *BankRowsHandler) HandleAutoAssignPreview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	preview, err := h.svc.PreviewBankAutoAssign(ctx, claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("bank auto-assign preview")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, preview)
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
	amountOverride, _ := strconv.ParseFloat(r.URL.Query().Get("amount"), 64)
	res, err := h.svc.PotentialInvoicesForRow(ctx, claims.ClubID, rowID, r.URL.Query().Get("q"), limit, amountOverride)
	if err != nil {
		h.log.Warn().Err(err).Str("row_id", rowID).Msg("potential invoices")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	JSON(w, http.StatusOK, map[string]any{"items": res, "potential": true})
}

type assignInvoiceReq struct {
	InvoiceID string `json:"invoice_id"`
	// RememberPayer saves the row's payer as an alias of the faktura's
	// member (BRY-383). A payer that is another member's name or alias is
	// refused with 409 alias_conflict, nothing booked, unless
	// ConfirmAliasOverride is set.
	RememberPayer        bool `json:"remember_payer"`
	ConfirmAliasOverride bool `json:"confirm_alias_override"`
}

func (r assignInvoiceReq) alias() accounting.PayerAliasRequest {
	return accounting.PayerAliasRequest{Remember: r.RememberPayer, ConfirmOverride: r.ConfirmAliasOverride}
}

// writeAliasConflict answers a refused payer alias. It names no member: the
// name belonging to someone else is all the treasurer needs to decide.
func writeAliasConflict(w http.ResponseWriter) {
	JSON(w, http.StatusConflict, map[string]any{
		"error":   accounting.ErrAliasConflict.Error(),
		"message": "Namnet høyrer alt til eit anna medlem. Stadfest at betalaren skal hugsast for dette medlemmet, eller tildel utan å hugsa.",
	})
}

// logTaughtAlias audits an alias a faktura assign saved.
func (h *BankRowsHandler) logTaughtAlias(r *http.Request, clubID, actorID string, a *accounting.TaughtAlias) {
	if h.audit == nil || a == nil {
		return
	}
	h.audit.LogAction(r.Context(), clubID, actorID, r.RemoteAddr,
		audit.ActionPayerAliasTaught, "vipps_customer_alias", a.ID,
		map[string]any{
			"user_id":  a.UserID,
			"source":   accounting.PayerAliasSourceBankTildel,
			"override": a.Override,
		})
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
	journalID, taught, err := h.svc.AssignBankRowToInvoiceRemembering(ctx, claims.ClubID, claims.UserID, rowID, req.InvoiceID, req.alias())
	if errors.Is(err, accounting.ErrAliasConflict) {
		writeAliasConflict(w)
		return
	}
	if err != nil {
		h.log.Warn().Err(err).Str("row_id", rowID).Msg("assign invoice")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	h.logTaughtAlias(r, claims.ClubID, claims.UserID, taught)
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
	// As on assignInvoiceReq; only saved when every row has the same payer.
	RememberPayer        bool `json:"remember_payer"`
	ConfirmAliasOverride bool `json:"confirm_alias_override"`
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
	journalIDs, taught, err := h.svc.AssignMultipleBankRowsToInvoiceRemembering(ctx, claims.ClubID, claims.UserID, req.RowIDs, req.InvoiceID,
		accounting.PayerAliasRequest{Remember: req.RememberPayer, ConfirmOverride: req.ConfirmAliasOverride})
	if errors.Is(err, accounting.ErrAliasConflict) {
		writeAliasConflict(w)
		return
	}
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
	h.logTaughtAlias(r, claims.ClubID, claims.UserID, taught)
	JSON(w, http.StatusOK, map[string]any{"journal_entry_ids": journalIDs})
}

type assignAccountReq struct {
	AccountCode string `json:"account_code"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	// ConfirmAlone books the row on its own even though it has a strong
	// partner row (BRY-380). Without it such a row is refused with 409.
	ConfirmAlone bool `json:"confirm_alone"`
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
	if !req.ConfirmAlone {
		partners, err := h.svc.StrongBankRowPartners(ctx, claims.ClubID, rowID)
		switch {
		case errors.Is(err, accounting.ErrBankRowNotFound):
			Error(w, http.StatusNotFound, err.Error())
			return
		case err != nil:
			h.log.Error().Err(err).Str("row_id", rowID).Msg("assign account: partner check")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		case len(partners) > 0:
			JSON(w, http.StatusConflict, map[string]any{
				"error":    "has_partner",
				"message":  "Denne rada ser ut til å høyra saman med ei anna. Bokfør dei saman, eller stadfest at ho skal bokførast åleine.",
				"partners": partners,
			})
			return
		}
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
				"confirm_alone":    req.ConfirmAlone,
			})
	}
	JSON(w, http.StatusOK, map[string]any{"journal_entry_id": journalID})
}

// HandlePartners lists the rows suggested as belonging with this one.
func (h *BankRowsHandler) HandlePartners(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rowID := chi.URLParam(r, "rowID")
	partners, err := h.svc.BankRowPartners(ctx, claims.ClubID, rowID)
	if err != nil {
		if errors.Is(err, accounting.ErrBankRowNotFound) {
			Error(w, http.StatusNotFound, err.Error())
			return
		}
		h.log.Warn().Err(err).Str("row_id", rowID).Msg("bank row partners")
		Error(w, http.StatusBadRequest, "could not load partner rows")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"items": partners})
}

type assignAccountGroupReq struct {
	RowIDs      []string `json:"row_ids"`
	AccountCode string   `json:"account_code"`
	Description string   `json:"description"`
}

// HandleAssignAccountGroup books rows that belong together as one bilag.
func (h *BankRowsHandler) HandleAssignAccountGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req assignAccountGroupReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AccountCode == "" || len(req.RowIDs) < 2 {
		Error(w, http.StatusBadRequest, "account_code and at least two row_ids required")
		return
	}
	res, err := h.svc.AssignBankRowsToAccountGroup(ctx, claims.ClubID, claims.UserID, req.RowIDs, req.AccountCode, req.Description)
	if err != nil {
		if errors.Is(err, accounting.ErrBankRowNotFound) {
			Error(w, http.StatusNotFound, err.Error())
			return
		}
		h.log.Warn().Err(err).Strs("row_ids", req.RowIDs).Msg("assign account group")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.audit != nil {
		for _, id := range res.RowIDs {
			h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
				audit.ActionBankRowAssignedAccount, "bank_import_row", id,
				map[string]any{
					"account_code":     req.AccountCode,
					"kind":             "group",
					"description":      req.Description,
					"journal_entry_id": res.JournalEntryID,
					"group_row_ids":    res.RowIDs,
					"net":              res.Net,
				})
		}
	}
	JSON(w, http.StatusOK, res)
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
	res, err := h.svc.UnassignBankRowReported(ctx, claims.ClubID, claims.UserID, rowID)
	if err != nil {
		switch {
		case errors.Is(err, accounting.ErrBankRowNotFound):
			Error(w, http.StatusNotFound, err.Error())
		case errors.Is(err, accounting.ErrPeriodClosed):
			writePeriodClosed(w, err)
		default:
			Error(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionBankRowUnassigned, "bank_import_row", rowID,
			map[string]any{
				"prior_journal_entry_id": res.PriorJournalID,
				"prior_invoice_id":       res.PriorInvoiceID,
				"sibling_row_ids":        res.SiblingRowIDs,
			})
	}
	JSON(w, http.StatusOK, map[string]string{"status": "unassigned"})
}

func (h *BankRowsHandler) HandleListPendingRefunds(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rows, err := h.svc.ListPendingRefunds(ctx, claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("list pending refunds")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if rows == nil {
		rows = []accounting.RefundPendingRow{}
	}
	JSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *BankRowsHandler) HandleCountPendingRefunds(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	n, err := h.svc.CountPendingRefunds(ctx, claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("count pending refunds")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"count": n})
}

func (h *BankRowsHandler) HandleSuggestRefundOutbound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rowID := chi.URLParam(r, "rowID")
	candidates, err := h.svc.SuggestRefundOutbound(ctx, claims.ClubID, rowID)
	if err != nil {
		h.log.Warn().Err(err).Str("row_id", rowID).Msg("suggest refund outbound")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if candidates == nil {
		candidates = []accounting.RefundOutboundCandidate{}
	}
	JSON(w, http.StatusOK, map[string]any{"items": candidates})
}

type pairRefundReq struct {
	OutboundRowID string `json:"outbound_row_id"`
}

func (h *BankRowsHandler) HandlePairRefund(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rowID := chi.URLParam(r, "rowID")
	var req pairRefundReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OutboundRowID == "" {
		Error(w, http.StatusBadRequest, "outbound_row_id required")
		return
	}
	inboundJournal, outboundJournal, err := h.svc.PairRefund(ctx, claims.ClubID, claims.UserID, rowID, req.OutboundRowID)
	if err != nil {
		h.log.Warn().Err(err).Str("inbound_row_id", rowID).Str("outbound_row_id", req.OutboundRowID).Msg("pair refund")
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionBankRowRefundPaired, "bank_import_row", rowID,
			map[string]any{
				"outbound_row_id":     req.OutboundRowID,
				"inbound_journal_id":  inboundJournal,
				"outbound_journal_id": outboundJournal,
			})
	}
	JSON(w, http.StatusOK, map[string]any{
		"inbound_journal_entry_id":  inboundJournal,
		"outbound_journal_entry_id": outboundJournal,
	})
}
