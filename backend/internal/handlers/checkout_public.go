package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/payments"
)

// Public token-addressed checkout API backing the /betal/{token} pay page
// (BRY-263). The token is the capability; no auth. A checkout is an `orders`
// row that settles a faktura (order_sources) and can carry ad-hoc catalog
// add-ons (order_lines). Billing-only price_items are never offered as add-ons.
var checkoutCatalogExcludedCategories = []string{"harbor_membership", "slip_fee", "broadcast"}

type checkoutLineResp struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Quantity   int     `json:"quantity"`
	UnitAmount float64 `json:"unit_amount"`
	Total      float64 `json:"total"`
}

type checkoutInvoiceResp struct {
	Number int                `json:"number"`
	Amount float64            `json:"amount"`
	Lines  []checkoutLineResp `json:"lines"`
}

type checkoutCatalogItem struct {
	PriceItemID string  `json:"price_item_id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Amount      float64 `json:"amount"`
	Category    string  `json:"category"`
}

type checkoutResp struct {
	Status      string                `json:"status"`
	Currency    string                `json:"currency"`
	Invoice     *checkoutInvoiceResp  `json:"invoice"`
	Lines       []checkoutLineResp    `json:"lines"`
	Total       float64               `json:"total"`
	Catalog     []checkoutCatalogItem `json:"catalog"`
	AlreadyPaid bool                  `json:"already_paid"`
	// PaidAmount is what the payment that settled the order charged, for the
	// receipt page; 0 until the order is paid. Total is what is still owed,
	// which a paid faktura keeps until settlement is imported.
	PaidAmount float64 `json:"paid_amount"`
	// OnlineBlocked is set when an unpaid source may not be paid online (over
	// the club's amount cap, forced off, or an imported receivable not yet in
	// the GL; BRY-283): the page hides the pay buttons and shows KID + Konto for
	// bank transfer instead. KID/BankAccount are populated so it can.
	OnlineBlocked bool   `json:"online_blocked"`
	KID           string `json:"kid_number"`
	BankAccount   string `json:"bank_account"`
	// Providers are the payment providers the payer can choose, in display
	// order (the default first). A provider whose minimum charge is above the
	// total is left out. Empty when none is configured, none charges the total,
	// or the order is blocked, which leaves bank transfer with BankAccount set.
	Providers []string `json:"providers"`
}

// checkoutByToken loads the order behind a pay token.
func (h *InvoiceHandler) checkoutByToken(ctx context.Context, token string) (orderID, clubID, status string, ok bool) {
	if token == "" {
		return "", "", "", false
	}
	if err := h.db.QueryRow(ctx,
		`SELECT id, club_id, status FROM orders WHERE token = $1`,
		token,
	).Scan(&orderID, &clubID, &status); err != nil {
		return "", "", "", false
	}
	return orderID, clubID, status, true
}

// HandleGetCheckout returns the full pay-page state: the faktura being settled
// (read-only), any ad-hoc add-ons, the running total, and the addable catalog.
func (h *InvoiceHandler) HandleGetCheckout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orderID, clubID, status, ok := h.checkoutByToken(ctx, chi.URLParam(r, "token"))
	if !ok {
		Error(w, http.StatusNotFound, "checkout not found")
		return
	}
	resp, err := h.buildCheckoutResp(ctx, orderID, clubID, status)
	if err != nil {
		h.log.Error().Err(err).Str("order_id", orderID).Msg("checkout: build response failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, resp)
}

func (h *InvoiceHandler) buildCheckoutResp(ctx context.Context, orderID, clubID, status string) (*checkoutResp, error) {
	resp := &checkoutResp{
		Status:      status,
		Currency:    "NOK",
		Lines:       []checkoutLineResp{},
		Catalog:     []checkoutCatalogItem{},
		AlreadyPaid: status == "paid" || status == "refunded",
	}
	if resp.AlreadyPaid {
		if err := h.db.QueryRow(ctx,
			`SELECT total_amount::float8 FROM orders WHERE id = $1`, orderID,
		).Scan(&resp.PaidAmount); err != nil {
			return nil, err
		}
	}

	inv, kid, err := h.checkoutInvoice(ctx, orderID)
	if err != nil {
		return nil, err
	}
	resp.Invoice, resp.KID = inv, kid

	// Ad-hoc add-ons.
	lineRows, err := h.db.Query(ctx,
		`SELECT id, name, quantity, unit_price, total_price
		   FROM order_lines WHERE order_id = $1 ORDER BY created_at`,
		orderID,
	)
	if err != nil {
		return nil, err
	}
	for lineRows.Next() {
		var l checkoutLineResp
		if err := lineRows.Scan(&l.ID, &l.Name, &l.Quantity, &l.UnitAmount, &l.Total); err != nil {
			lineRows.Close()
			return nil, err
		}
		resp.Lines = append(resp.Lines, l)
	}
	lineRows.Close()
	if err := lineRows.Err(); err != nil {
		return nil, err
	}

	// Running total (unpaid invoice sources + ad-hoc lines).
	total, _, err := h.orderTotal(ctx, orderID)
	if err != nil {
		return nil, err
	}
	resp.Total = total

	// Addable catalog: active price_items minus billing-only categories.
	catRows, err := h.db.Query(ctx,
		`SELECT id, name, description, amount, category
		   FROM price_items
		  WHERE club_id = $1 AND is_active = true
		    AND category <> ALL($2)
		  ORDER BY sort_order, name`,
		clubID, checkoutCatalogExcludedCategories,
	)
	if err != nil {
		return nil, err
	}
	defer catRows.Close()
	for catRows.Next() {
		var c checkoutCatalogItem
		if err := catRows.Scan(&c.PriceItemID, &c.Name, &c.Description, &c.Amount, &c.Category); err != nil {
			return nil, err
		}
		resp.Catalog = append(resp.Catalog, c)
	}
	if err := catRows.Err(); err != nil {
		return nil, err
	}

	// If an unpaid source isn't payable online, the page must steer to bank
	// transfer: flag it so no pay button shows (BRY-283). With no provider to
	// offer, for either reason, surface KID + konto; KID is already loaded
	// above, so load the club's default account.
	blocked, err := h.orderHasOnlineBlockedSource(ctx, orderID)
	if err != nil {
		return nil, err
	}
	resp.OnlineBlocked = blocked
	resp.Providers = []string{}
	if !blocked {
		providers := h.availableProviders()
		if total > 0 {
			// Same rounding as the payment request, so a total the page offers
			// a provider for is one that provider accepts.
			providers = h.providersFor(toOre(total))
		}
		resp.Providers = append(resp.Providers, providers...)
	}
	if len(resp.Providers) == 0 {
		club, err := loadClubInvoiceFields(ctx, h.db, clubID)
		if err != nil {
			return nil, err
		}
		resp.BankAccount = club.BankAccount
	}

	return resp, nil
}

// checkoutInvoice loads the faktura a checkout settles (phase 1: a single
// invoice source) with its lines and KID; nil when the order has none.
func (h *InvoiceHandler) checkoutInvoice(ctx context.Context, orderID string) (*checkoutInvoiceResp, string, error) {
	var (
		invoiceID     string
		invoiceNumber int
		invoiceAmount float64
		kid           string
	)
	err := h.db.QueryRow(ctx,
		`SELECT i.id, i.invoice_number, i.total_amount, COALESCE(i.kid_number, '')
		   FROM order_sources os
		   JOIN invoices i ON i.id = os.invoice_id
		  WHERE os.order_id = $1
		  ORDER BY i.invoice_number
		  LIMIT 1`,
		orderID,
	).Scan(&invoiceID, &invoiceNumber, &invoiceAmount, &kid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	inv := &checkoutInvoiceResp{Number: invoiceNumber, Amount: invoiceAmount, Lines: []checkoutLineResp{}}
	rows, err := h.db.Query(ctx,
		`SELECT description, quantity, unit_price, line_total
		   FROM invoice_lines WHERE invoice_id = $1 ORDER BY id`,
		invoiceID,
	)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	for rows.Next() {
		var l checkoutLineResp
		if err := rows.Scan(&l.Name, &l.Quantity, &l.UnitAmount, &l.Total); err != nil {
			return nil, "", err
		}
		inv.Lines = append(inv.Lines, l)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	return inv, kid, nil
}

type addCheckoutLineRequest struct {
	PriceItemID string `json:"price_item_id"`
	Quantity    int    `json:"quantity"`
}

// HandleAddCheckoutLine adds an ad-hoc catalog line to the checkout. Only
// allowed while the checkout is unpaid; the faktura itself is never mutated.
func (h *InvoiceHandler) HandleAddCheckoutLine(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orderID, clubID, status, ok := h.checkoutByToken(ctx, chi.URLParam(r, "token"))
	if !ok {
		Error(w, http.StatusNotFound, "checkout not found")
		return
	}
	if status != "pending" {
		Error(w, http.StatusConflict, "checkout is already settled")
		return
	}

	var req addCheckoutLineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Quantity < 1 {
		req.Quantity = 1
	}

	// Resolve the price item — must be active, this club's, and not a
	// billing-only category (members can't self-add a membership fee).
	var (
		name     string
		amount   float64
		category string
	)
	err := h.db.QueryRow(ctx,
		`SELECT name, amount, category FROM price_items
		  WHERE id = $1 AND club_id = $2 AND is_active = true`,
		req.PriceItemID, clubID,
	).Scan(&name, &amount, &category)
	if errors.Is(err, pgx.ErrNoRows) {
		Error(w, http.StatusBadRequest, "unknown or unavailable item")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Msg("checkout: resolve price item failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	for _, c := range checkoutCatalogExcludedCategories {
		if category == c {
			Error(w, http.StatusBadRequest, "item is not available for self-service purchase")
			return
		}
	}

	lineTotal := amount * float64(req.Quantity)
	if _, err := h.db.Exec(ctx,
		`INSERT INTO order_lines (order_id, price_item_id, name, quantity, unit_price, total_price)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		orderID, req.PriceItemID, name, req.Quantity, amount, lineTotal,
	); err != nil {
		h.log.Error().Err(err).Msg("checkout: insert order line failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	resp, err := h.buildCheckoutResp(ctx, orderID, clubID, status)
	if err != nil {
		h.log.Error().Err(err).Msg("checkout: rebuild after add failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, resp)
}

// HandleRemoveCheckoutLine deletes an ad-hoc add-on. Invoice lines are not
// order_lines, so this can never touch the faktura.
func (h *InvoiceHandler) HandleRemoveCheckoutLine(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orderID, clubID, status, ok := h.checkoutByToken(ctx, chi.URLParam(r, "token"))
	if !ok {
		Error(w, http.StatusNotFound, "checkout not found")
		return
	}
	if status != "pending" {
		Error(w, http.StatusConflict, "checkout is already settled")
		return
	}
	lineID := chi.URLParam(r, "lineID")
	if _, err := h.db.Exec(ctx,
		`DELETE FROM order_lines WHERE id = $1 AND order_id = $2`,
		lineID, orderID,
	); err != nil {
		h.log.Error().Err(err).Msg("checkout: delete order line failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp, err := h.buildCheckoutResp(ctx, orderID, clubID, status)
	if err != nil {
		h.log.Error().Err(err).Msg("checkout: rebuild after remove failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	JSON(w, http.StatusOK, resp)
}

// payCheckoutRequest is the optional body of POST /checkout/{token}/pay.
type payCheckoutRequest struct {
	// Provider picks the payment provider; empty uses the default.
	Provider string `json:"provider"`
}

// HandlePayCheckout mints a fresh payment for the current total and returns
// the provider's checkout URL for the browser to follow.
func (h *InvoiceHandler) HandlePayCheckout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := chi.URLParam(r, "token")
	orderID, _, status, ok := h.checkoutByToken(ctx, token)
	if !ok {
		Error(w, http.StatusNotFound, "checkout not found")
		return
	}
	if status == "paid" || status == "refunded" {
		Error(w, http.StatusConflict, "checkout is already paid")
		return
	}
	if !h.providers.Configured() {
		Error(w, http.StatusServiceUnavailable, msgOnlineUnavailable)
		return
	}
	var req payCheckoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	provider, err := h.resolveProvider(req.Provider)
	if err != nil {
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	redirectURL, err := h.mintOrderPayment(ctx, provider, orderID, h.checkoutPageURL(token))
	if errors.Is(err, errNothingToPay) {
		Error(w, http.StatusBadRequest, "nothing to pay")
		return
	}
	if errors.Is(err, errUnsupportedCurrency) {
		Error(w, http.StatusConflict, errCodeUnsupportedCurrency)
		return
	}
	if errors.Is(err, errOnlinePaymentBlocked) {
		// Not payable online → bank transfer only (BRY-283). The page already
		// hides the buttons via online_blocked; this guards a stale client.
		Error(w, http.StatusConflict, errCodeOnlineOverLimit)
		return
	}
	if errors.Is(err, payments.ErrAmountBelowMinimum) {
		// The page leaves such a provider out; this guards a stale client or
		// a provider whose own minimum is higher than its table.
		Error(w, http.StatusConflict, errCodeAmountBelowMinimum)
		return
	}
	if err != nil {
		h.log.Error().Err(err).Str("order_id", orderID).Msg("checkout: mint payment failed")
		Error(w, http.StatusBadGateway, msgPaymentStartFailed)
		return
	}
	JSON(w, http.StatusOK, map[string]string{"redirect_url": redirectURL})
}
