package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/brygge-klubb/brygge/internal/accounting"
	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/shared"
)

// The manual actions on a Stripe balance transaction in review: assign to
// a faktura, assign to an account, dismiss and undismiss
// (accounting/stripe_manual.go). The list carries each row's
// allowed_actions, so the UI offers only what the server accepts.

// HandleListStripeTransactions is GET
// /accounting/stripe/transactions?status=needs_review|dismissed&limit=&offset=:
// the club's Stripe rows in that state (needs_review by default), newest
// first, each with its allowed_actions. Read only, so open to every
// accounting role.
func (h *AccountingHandler) HandleListStripeTransactions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = accounting.StripeTxnNeedsReview
	}
	if status != accounting.StripeTxnNeedsReview && status != accounting.StripeTxnDismissed {
		Error(w, http.StatusBadRequest, "status must be needs_review or dismissed")
		return
	}
	pg := shared.ParsePagination(r, 50, 200)
	items, hasMore, err := h.svc.ListStripeTransactions(ctx, claims.ClubID, status, pg.Limit, pg.Offset)
	if err != nil {
		h.log.Error().Err(err).Msg("listing stripe transactions failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, shared.PaginatedResponse{Items: items, Limit: pg.Limit, Offset: pg.Offset, HasMore: hasMore})
}

// HandleStripeTxnPotentialInvoices is GET
// /accounting/stripe/transactions/{txnID}/potential-invoices?q=&limit=:
// open unpaid fakturas whose total equals the charge.
func (h *AccountingHandler) HandleStripeTxnPotentialInvoices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	txnID, ok := stripeTxnIDParam(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.svc.PotentialInvoicesForStripeTxn(ctx, claims.ClubID, txnID, r.URL.Query().Get("q"), limit)
	if errors.Is(err, accounting.ErrStripeTxnNotFound) {
		Error(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		h.log.Error().Err(err).Str("txn_id", txnID).Msg("stripe transaction potential invoices failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"items": items, "potential": true})
}

// HandleListStripeAssignableAccounts is GET
// /accounting/stripe/assignable-accounts: the active accounts a Stripe row
// may be booked against by hand.
func (h *AccountingHandler) HandleListStripeAssignableAccounts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	items, err := h.svc.ListStripeAssignableAccounts(ctx, claims.ClubID)
	if err != nil {
		h.log.Error().Err(err).Msg("listing stripe assignable accounts failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"items": items})
}

type stripeAssignInvoiceReq struct {
	InvoiceID string `json:"invoice_id"`
}

// HandleAssignStripeTxnToInvoice is POST
// /accounting/stripe/transactions/{txnID}/assign-invoice {invoice_id}.
// Audited stripe_txn.assigned_invoice.
func (h *AccountingHandler) HandleAssignStripeTxnToInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	txnID, ok := stripeTxnIDParam(w, r)
	if !ok {
		return
	}
	var req stripeAssignInvoiceReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.InvoiceID == "" {
		Error(w, http.StatusBadRequest, "invoice_id required")
		return
	}
	if _, err := uuid.Parse(req.InvoiceID); err != nil {
		Error(w, http.StatusNotFound, accounting.ErrStripeInvoiceNotFound.Error())
		return
	}
	res, err := h.svc.AssignStripeTxnToInvoice(ctx, claims.ClubID, claims.UserID, txnID, req.InvoiceID)
	if h.stripeTxnFailed(w, err, txnID, "assign stripe transaction to invoice") {
		return
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionStripeTxnAssignedInvoice, "stripe_balance_transaction", txnID,
			map[string]any{
				"balance_transaction_id": res.BalanceTransactionID,
				"invoice_id":             res.InvoiceID,
				"journal_entry_id":       res.JournalEntryID,
			})
	}
	JSON(w, http.StatusOK, res)
}

type stripeAssignAccountReq struct {
	AccountCode string `json:"account_code"`
	Note        string `json:"note"`
}

// HandleAssignStripeTxnToAccount is POST
// /accounting/stripe/transactions/{txnID}/assign-account {account_code,
// note}. Audited stripe_txn.assigned_account.
func (h *AccountingHandler) HandleAssignStripeTxnToAccount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	txnID, ok := stripeTxnIDParam(w, r)
	if !ok {
		return
	}
	var req stripeAssignAccountReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.AccountCode) == "" {
		Error(w, http.StatusBadRequest, "account_code required")
		return
	}
	res, err := h.svc.AssignStripeTxnToAccount(ctx, claims.ClubID, claims.UserID, txnID, req.AccountCode, req.Note)
	if h.stripeTxnFailed(w, err, txnID, "assign stripe transaction to account") {
		return
	}
	if h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionStripeTxnAssignedAccount, "stripe_balance_transaction", txnID,
			map[string]any{
				"balance_transaction_id": res.BalanceTransactionID,
				"account_code":           res.AccountCode,
				"note":                   req.Note,
				"journal_entry_id":       res.JournalEntryID,
			})
	}
	JSON(w, http.StatusOK, res)
}

type stripeDismissReq struct {
	Reason string `json:"reason"`
	Note   string `json:"note"`
}

// HandleDismissStripeTxn is POST
// /accounting/stripe/transactions/{txnID}/dismiss {reason, note}. Audited
// stripe_txn.dismissed unless the row was dismissed already.
func (h *AccountingHandler) HandleDismissStripeTxn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	txnID, ok := stripeTxnIDParam(w, r)
	if !ok {
		return
	}
	var req stripeDismissReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	res, err := h.svc.DismissStripeTxn(ctx, claims.ClubID, claims.UserID, txnID, req.Reason, req.Note)
	if h.stripeTxnFailed(w, err, txnID, "dismiss stripe transaction") {
		return
	}
	if h.audit != nil && !res.Unchanged {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionStripeTxnDismissed, "stripe_balance_transaction", txnID,
			map[string]any{
				"balance_transaction_id": res.BalanceTransactionID,
				"reason":                 req.Reason,
				"note":                   req.Note,
				"review_reason":          res.ReviewReason,
			})
	}
	JSON(w, http.StatusOK, res)
}

// HandleUndismissStripeTxn is POST
// /accounting/stripe/transactions/{txnID}/undismiss. Audited
// stripe_txn.undismissed unless the row was not dismissed.
func (h *AccountingHandler) HandleUndismissStripeTxn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	txnID, ok := stripeTxnIDParam(w, r)
	if !ok {
		return
	}
	res, err := h.svc.UndismissStripeTxn(ctx, claims.ClubID, claims.UserID, txnID)
	if h.stripeTxnFailed(w, err, txnID, "undismiss stripe transaction") {
		return
	}
	if h.audit != nil && !res.Unchanged {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionStripeTxnUndismissed, "stripe_balance_transaction", txnID,
			map[string]any{
				"balance_transaction_id": res.BalanceTransactionID,
				"status":                 res.Status,
				"review_reason":          res.ReviewReason,
			})
	}
	JSON(w, http.StatusOK, res)
}

// stripeTxnIDParam reads {txnID}; an id that is not a uuid is answered 404.
func stripeTxnIDParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "txnID")
	if _, err := uuid.Parse(id); err != nil {
		Error(w, http.StatusNotFound, accounting.ErrStripeTxnNotFound.Error())
		return "", false
	}
	return id, true
}

// stripeTxnFailed answers a failed manual action and reports whether it
// did: a refusal is 409 with its code and Norwegian reason, a Stripe slot
// still busy 409 busy, an unknown row or faktura 404.
func (h *AccountingHandler) stripeTxnFailed(w http.ResponseWriter, err error, txnID, what string) bool {
	var refusal *accounting.StripeTxnRefusal
	switch {
	case err == nil:
		return false
	case errors.As(err, &refusal):
		JSON(w, http.StatusConflict, map[string]string{"error": refusal.Error(), "code": refusal.Code, "reason": refusal.Reason})
	case errors.Is(err, accounting.ErrStripeSyncBusy):
		JSON(w, http.StatusConflict, map[string]string{
			"error": "A Stripe sync is already running; try again shortly", "code": stripeBusyCode,
		})
	case errors.Is(err, accounting.ErrStripeTxnNotFound), errors.Is(err, accounting.ErrStripeInvoiceNotFound):
		Error(w, http.StatusNotFound, err.Error())
	default:
		h.log.Error().Err(err).Str("txn_id", txnID).Msg(what + " failed")
		Error(w, http.StatusInternalServerError, "internal error")
	}
	return true
}
