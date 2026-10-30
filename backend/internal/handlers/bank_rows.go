package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
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
	if errors.Is(err, accounting.ErrInvalidBankRowKind) {
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
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
	n, err := h.svc.CountUnmatchedBankRowsKind(ctx, claims.ClubID, r.URL.Query().Get("kind"), year)
	if errors.Is(err, accounting.ErrInvalidBankRowKind) {
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
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

// importedNoOpeningBalanceCode is the 409 code for a payment refused because
// its faktura was imported and its receivable isn't in the GL yet (BRY-386).
const importedNoOpeningBalanceCode = "imported_no_opening_balance"

// importedNoOpeningBalanceConflict answers 409 for
// accounting.ErrImportedNoOpeningBalance. The message is club-agnostic and
// shown as is; the code lets the SPA word it in the treasurer's locale.
func importedNoOpeningBalanceConflict(w http.ResponseWriter) {
	JSON(w, http.StatusConflict, map[string]string{
		"error": accounting.ErrImportedNoOpeningBalance.Error(),
		"code":  importedNoOpeningBalanceCode,
	})
}

// The 409 codes for a payment refused because its faktura is credited in
// full, is a credit note (BRY-508), or is voided. The Stripe assign answers
// invoice_credited and invoice_not_debt through accounting.StripeTxnRefusal;
// a voided faktura is its invoice_settled.
const (
	invoiceCreditedCode = "invoice_credited"
	invoiceNotDebtCode  = "invoice_not_debt"
	invoiceVoidedCode   = "invoice_voided"
	// settlementOverpaidCode is the legacy Vipps confirm's refusal of lines
	// that link one faktura twice or credit it above its total.
	settlementOverpaidCode = "settlement_invoice_overpaid"
	// vippsRowBookedCode is the confirm's refusal of a settlement with a
	// Vipps row that is already booked.
	vippsRowBookedCode = "vipps_row_booked"
)

// invoiceNotAssignableConflict answers 409 invoice_credited, invoice_not_debt
// or invoice_voided, nothing booked, when err is accounting.ErrInvoiceCredited,
// accounting.ErrInvoiceNotDebt or accounting.ErrInvoiceVoided, and reports
// whether it did. The message is
// Norwegian and shown as is; the code lets the SPA word it in the
// treasurer's locale.
func invoiceNotAssignableConflict(w http.ResponseWriter, err error) bool {
	for _, refusal := range []struct {
		err  error
		code string
	}{
		{accounting.ErrInvoiceCredited, invoiceCreditedCode},
		{accounting.ErrInvoiceNotDebt, invoiceNotDebtCode},
		{accounting.ErrInvoiceVoided, invoiceVoidedCode},
		{accounting.ErrSettlementInvoiceOverpaid, settlementOverpaidCode},
		{accounting.ErrVippsRowAlreadyBooked, vippsRowBookedCode},
	} {
		if errors.Is(err, refusal.err) {
			JSON(w, http.StatusConflict, map[string]string{"error": refusal.err.Error(), "code": refusal.code})
			return true
		}
	}
	return false
}

// providerPayoutCode is the 409 code for a manual assign of a provider
// payout deposit (accounting.ProviderPayoutDepositError).
const providerPayoutCode = "provider_payout"

// providerPayoutConflict answers 409 provider_payout, with the provider
// and the row, when err refuses a provider payout deposit, and reports
// whether it did.
func providerPayoutConflict(w http.ResponseWriter, err error) bool {
	var refusal *accounting.ProviderPayoutDepositError
	if !errors.As(err, &refusal) {
		return false
	}
	JSON(w, http.StatusConflict, map[string]string{
		"error":       refusal.Error(),
		"code":        providerPayoutCode,
		"provider":    refusal.Kind,
		"bank_row_id": refusal.RowID,
	})
	return true
}

// referencePeriodCode is the 409 code for a booking refused because it
// would put anything but a cost into a reference period (BRY-418).
const referencePeriodCode = "reference_period"

// writeReferencePeriod answers 409 reference_period. The message is
// Norwegian and shown as is; the code lets the SPA word it in the
// treasurer's locale.
func writeReferencePeriod(w http.ResponseWriter) {
	JSON(w, http.StatusConflict, map[string]string{
		"error": accounting.ErrReferencePeriod.Error(),
		"code":  referencePeriodCode,
	})
}

// referencePeriodConflict answers 409 reference_period when err is
// accounting.ErrReferencePeriod, and reports whether it did.
func referencePeriodConflict(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, accounting.ErrReferencePeriod) {
		return false
	}
	writeReferencePeriod(w)
	return true
}

func (r assignInvoiceReq) alias() accounting.PayerAliasRequest {
	return accounting.PayerAliasRequest{Remember: r.RememberPayer, ConfirmOverride: r.ConfirmAliasOverride}
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
	if referencePeriodConflict(w, err) {
		return
	}
	if errors.Is(err, accounting.ErrAliasConflict) {
		// The code alone: the SPA words it, and names no member.
		Error(w, http.StatusConflict, accounting.ErrAliasConflict.Error())
		return
	}
	if errors.Is(err, accounting.ErrImportedNoOpeningBalance) {
		importedNoOpeningBalanceConflict(w)
		return
	}
	if invoiceNotAssignableConflict(w, err) {
		return
	}
	if providerPayoutConflict(w, err) {
		return
	}
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
	h.logTaughtAlias(r, claims.ClubID, claims.UserID, taught)
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
	if referencePeriodConflict(w, err) {
		return
	}
	if errors.Is(err, accounting.ErrAliasConflict) {
		// The code alone: the SPA words it, and names no member.
		Error(w, http.StatusConflict, accounting.ErrAliasConflict.Error())
		return
	}
	if errors.Is(err, accounting.ErrImportedNoOpeningBalance) {
		importedNoOpeningBalanceConflict(w)
		return
	}
	if invoiceNotAssignableConflict(w, err) {
		return
	}
	if providerPayoutConflict(w, err) {
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
	if referencePeriodConflict(w, err) {
		return
	}
	if providerPayoutConflict(w, err) {
		return
	}
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
	if referencePeriodConflict(w, err) {
		return
	}
	if providerPayoutConflict(w, err) {
		return
	}
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

// HandleUndismiss returns a dismissed reference-year row to the Referanseår
// list (BRY-418). Dismissals elsewhere are permanent.
func (h *BankRowsHandler) HandleUndismiss(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	rowID := chi.URLParam(r, "rowID")
	if _, err := uuid.Parse(rowID); err != nil {
		Error(w, http.StatusNotFound, "bank row not found")
		return
	}
	prior, err := h.svc.UndismissReferenceBankRow(ctx, claims.ClubID, rowID)
	if err != nil {
		if errors.Is(err, accounting.ErrBankRowNotUndismissable) {
			Error(w, http.StatusConflict, err.Error())
			return
		}
		h.log.Error().Err(err).Str("row_id", rowID).Msg("undismiss bank row")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionBankRowUndismissed, "bank_import_row", rowID,
			map[string]any{
				"prior_reason":       prior.Reason,
				"prior_dismissed_by": prior.By,
				"prior_dismissed_at": prior.At,
			})
	}
	JSON(w, http.StatusOK, map[string]string{"status": "undismissed"})
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
		case errors.Is(err, accounting.ErrReferencePeriod):
			writeReferencePeriod(w)
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
				"reopened_invoice_ids":   res.ReopenedInvoiceIDs,
				"released_vipps_row_ids": res.ReleasedVippsRowIDs,
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
	if referencePeriodConflict(w, err) {
		return
	}
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
