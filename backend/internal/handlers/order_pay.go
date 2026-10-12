package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

// errNothingToPay signals a checkout order whose invoice sources are all already
// settled and which carries no ad-hoc lines — the payer is sent back to the site
// instead of a zero-amount Vipps checkout.
var errNothingToPay = errors.New("order has nothing left to pay")

// errVippsOverLimit signals a checkout order whose unpaid invoice source exceeds
// the club's Vipps amount cap (BRY-283): the payer is steered to bank transfer
// (KID) instead of being charged via Vipps, so the club avoids the fee.
var errVippsOverLimit = errors.New("order exceeds the club's Vipps amount limit")

// ensureFakturaOrderToken returns the durable token of the faktura's checkout
// order, which the email links to. The order (an `orders` row with an
// order_sources link to the invoice) is created lazily the first time a faktura
// should carry a Vipps link (create=true) and reused forever after, so resends
// keep the same link. Returns "" when Vipps is unconfigured, the invoice is over
// the club's Vipps amount cap (BRY-283), or nothing should be offered.
//
// The ePayment itself is NOT minted here: its redirectUrl is only valid ~10
// minutes, so it is useless in a pay-later email. It is minted on click by
// HandleOrderPayRedirect. This only ensures the durable order + link exist.
func (h *InvoiceHandler) ensureFakturaOrderToken(ctx context.Context, clubID, invoiceID string, create bool) string {
	if h.vipps == nil {
		return ""
	}

	// Respect the club's Vipps amount cap: large invoices are bank-transfer only.
	// Checked before the reuse SELECT below so an invoice that already has a
	// durable order stops offering Vipps once the club sets a cap under it.
	if !h.invoiceVippsAllowed(ctx, clubID, invoiceID) {
		return ""
	}

	// Reuse the invoice's existing checkout order so resends keep the same link.
	var token string
	err := h.db.QueryRow(ctx,
		`SELECT o.token
		   FROM orders o
		   JOIN order_sources os ON os.order_id = o.id
		  WHERE os.invoice_id = $1 AND o.club_id = $2`,
		invoiceID, clubID,
	).Scan(&token)
	if err == nil {
		return token
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		h.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("order pay: load token failed")
		return ""
	}
	if !create {
		return ""
	}

	var (
		total  float64
		userID string
	)
	if err := h.db.QueryRow(ctx,
		`SELECT total_amount, user_id FROM invoices WHERE id = $1 AND club_id = $2`,
		invoiceID, clubID,
	).Scan(&total, &userID); err != nil {
		h.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("order pay: load invoice failed")
		return ""
	}

	token = genCheckoutToken()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("order pay: begin tx failed")
		return ""
	}
	defer tx.Rollback(ctx)

	var orderID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO orders (club_id, user_id, total_amount, token) VALUES ($1, $2, $3, $4) RETURNING id`,
		clubID, userID, total, token,
	).Scan(&orderID); err != nil {
		h.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("order pay: insert order failed")
		return ""
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO order_sources (order_id, invoice_id) VALUES ($1, $2)`,
		orderID, invoiceID,
	); err != nil {
		h.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("order pay: insert source failed")
		return ""
	}
	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("order pay: commit failed")
		return ""
	}
	return token
}

// invoiceVippsAllowed reports whether the club offers a Vipps link for this
// invoice: true unless the club set clubs.vipps_max_amount and the invoice's
// total_amount exceeds it (BRY-283). A NULL cap means no limit. On error it
// fails open (returns true) rather than silently suppressing a legitimate link.
func (h *InvoiceHandler) invoiceVippsAllowed(ctx context.Context, clubID, invoiceID string) bool {
	var overLimit bool
	err := h.db.QueryRow(ctx,
		`SELECT i.total_amount > c.vipps_max_amount
		   FROM invoices i
		   JOIN clubs c ON c.id = i.club_id
		  WHERE i.id = $1 AND i.club_id = $2 AND c.vipps_max_amount IS NOT NULL`,
		invoiceID, clubID,
	).Scan(&overLimit)
	if errors.Is(err, pgx.ErrNoRows) {
		// No row: the club has no cap (vipps_max_amount IS NULL) or the invoice
		// doesn't exist — either way, don't suppress here.
		return true
	}
	if err != nil {
		h.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("vipps limit check failed")
		return true
	}
	return !overLimit
}

// orderVippsOverLimit reports whether any still-unpaid invoice source of the
// order exceeds the club's Vipps amount cap (BRY-283). Used at mint time to
// refuse charging a link that was issued before the cap was set (or is already
// sitting in a member's inbox).
func (h *InvoiceHandler) orderVippsOverLimit(ctx context.Context, orderID string) (bool, error) {
	var over bool
	err := h.db.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1
		     FROM order_sources os
		     JOIN invoices i ON i.id = os.invoice_id
		     JOIN orders o   ON o.id = os.order_id
		     JOIN clubs c    ON c.id = o.club_id
		    WHERE os.order_id = $1
		      AND c.vipps_max_amount IS NOT NULL
		      AND i.total_amount > c.vipps_max_amount
		      AND i.payment_id IS NULL
		      AND i.vipps_paid_at IS NULL
		      AND i.status <> 'voided'
		 )`,
		orderID,
	).Scan(&over)
	return over, err
}

// orderPayURL builds the public, tokenised pay link put in the email. It points
// at the /betal/{token} checkout page (which itemises the faktura, lets the
// payer add catalog items, then mints a fresh ePayment on demand), not Vipps
// directly. Empty token → empty URL (KID-only email).
func (h *InvoiceHandler) orderPayURL(token string) string {
	if token == "" {
		return ""
	}
	return h.frontendBase() + "/betal/" + token
}

// frontendBase is the trimmed public site origin used to build member-facing
// links: the configured FrontendURL, falling back to https://<Domain>.
func (h *InvoiceHandler) frontendBase() string {
	base := h.config.FrontendURL
	if base == "" {
		base = "https://" + h.config.Domain
	}
	return strings.TrimRight(base, "/")
}

type invoicePayLinkResponse struct {
	PayURL string `json:"pay_url"`
}

// HandleGetInvoicePayLink returns the durable /betal pay link for one invoice so
// staff can hand it to a member directly (SMS, chat, read it out) rather than
// only via the emailed faktura. It ensures the checkout order exists (reusing it
// on later calls), refuses voided invoices, and returns 409 "vipps_unavailable"
// when Vipps isn't configured so the UI can hide the affordance gracefully.
func (h *InvoiceHandler) HandleGetInvoicePayLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	invoiceID := chi.URLParam(r, "invoiceID")
	if invoiceID == "" {
		Error(w, http.StatusBadRequest, "invoice ID is required")
		return
	}
	if h.vipps == nil {
		Error(w, http.StatusConflict, "vipps_unavailable")
		return
	}

	// Club-scope the invoice and block voided ones (paying a cancelled faktura
	// makes no sense; the /betal page already refuses already-settled orders).
	var status string
	err := h.db.QueryRow(ctx,
		`SELECT status FROM invoices WHERE id = $1 AND club_id = $2`,
		invoiceID, claims.ClubID,
	).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		Error(w, http.StatusNotFound, "invoice not found")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("pay-link: load invoice failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if status == "voided" {
		Error(w, http.StatusConflict, "invoice is voided")
		return
	}

	// Over the club's Vipps cap → bank transfer only; tell the UI distinctly so
	// it shows "over limit" rather than "Vipps not configured" (BRY-283).
	if !h.invoiceVippsAllowed(ctx, claims.ClubID, invoiceID) {
		Error(w, http.StatusConflict, "vipps_over_limit")
		return
	}

	url := h.orderPayURL(h.ensureFakturaOrderToken(ctx, claims.ClubID, invoiceID, true))
	if url == "" {
		Error(w, http.StatusConflict, "vipps_unavailable")
		return
	}
	JSON(w, http.StatusOK, invoicePayLinkResponse{PayURL: url})
}

// HandleOrderPayRedirect is the public GET /api/v1/betal/{token} endpoint the
// faktura email links to. It mints a fresh ePayment for the checkout order and
// 302-redirects to the Vipps checkout — the checkout URL lives only ~10 minutes
// so it can't be emailed directly. No auth: the token is the capability.
func (h *InvoiceHandler) HandleOrderPayRedirect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := chi.URLParam(r, "token")
	if token == "" {
		http.Error(w, "Ugyldig betalingslenke.", http.StatusBadRequest)
		return
	}

	var (
		orderID string
		status  string
	)
	if err := h.db.QueryRow(ctx,
		`SELECT id, status FROM orders WHERE token = $1`,
		token,
	).Scan(&orderID, &status); err != nil {
		http.Error(w, "Fant ikke betalingen.", http.StatusNotFound)
		return
	}

	// Already captured or refunded → don't charge again; send them to the site.
	// 'pending'/'failed' fall through so an abandoned or failed attempt can retry.
	if status == "paid" || status == "refunded" {
		http.Redirect(w, r, h.vippsReturnURL(), http.StatusFound)
		return
	}
	if h.vipps == nil {
		http.Error(w, "Vipps-betaling er ikke tilgjengelig akkurat nå.", http.StatusServiceUnavailable)
		return
	}

	redirectURL, err := h.mintOrderPayment(ctx, orderID)
	if errors.Is(err, errNothingToPay) {
		http.Redirect(w, r, h.vippsReturnURL(), http.StatusFound)
		return
	}
	if errors.Is(err, errVippsOverLimit) {
		// Over the club's Vipps cap → send them to the checkout page, which shows
		// KID + konto for bank transfer instead of a Vipps button (BRY-283).
		http.Redirect(w, r, h.frontendBase()+"/betal/"+token, http.StatusFound)
		return
	}
	if err != nil {
		h.log.Error().Err(err).Str("order_id", orderID).Msg("order pay redirect: mint payment failed")
		http.Error(w, "Kunne ikke starte Vipps-betaling. Prøv igjen.", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// mintOrderPayment sums the order's still-unpaid invoice sources and its ad-hoc
// lines, mints a fresh ePayment for the total, and stores its reference so the
// CAPTURED webhook can match it back. Returns the short-lived Vipps checkout
// URL, or errNothingToPay when nothing is outstanding.
func (h *InvoiceHandler) mintOrderPayment(ctx context.Context, orderID string) (string, error) {
	// Refuse to charge via Vipps when an unpaid source is over the club's cap —
	// closes the gap that a token issued before the cap (or already emailed)
	// would otherwise leave open (BRY-283).
	over, err := h.orderVippsOverLimit(ctx, orderID)
	if err != nil {
		return "", err
	}
	if over {
		return "", errVippsOverLimit
	}

	total, desc, err := h.orderTotal(ctx, orderID)
	if err != nil {
		return "", err
	}
	if total <= 0 {
		return "", errNothingToPay
	}

	// Guest checkout (QR / walk-up, no linked member): ask the payer to share
	// their name so the harbor overview shows who paid instead of "Gjest"
	// (BRY-272). Members paying their own faktura are skipped — no consent
	// friction, and we already know them.
	var (
		profile *vipps.Profile
		userID  *string
	)
	if err := h.db.QueryRow(ctx, `SELECT user_id FROM orders WHERE id = $1`, orderID).Scan(&userID); err == nil && userID == nil {
		profile = &vipps.Profile{Scope: "name"}
	}

	ref := genVippsReference()
	res, err := h.vipps.CreatePayment(ctx, ref, vipps.CreatePaymentRequest{
		Amount:             vipps.Amount{Currency: "NOK", Value: int64(math.Round(total * 100))},
		Reference:          ref,
		ReturnURL:          h.vippsReturnURL(),
		PaymentDescription: desc,
		UserFlow:           "WEB_REDIRECT",
		Profile:            profile,
	})
	if err != nil {
		return "", err
	}
	if _, err := h.db.Exec(ctx,
		`UPDATE orders SET vipps_reference = $1, total_amount = $2, updated_at = now() WHERE id = $3`,
		ref, total, orderID,
	); err != nil {
		// Payment exists at Vipps but the reference didn't persist — the webhook
		// won't match it. Surface loudly but still redirect the payer.
		h.log.Error().Err(err).Str("order_id", orderID).Str("vipps_reference", ref).
			Msg("order pay: created payment but failed to store reference — webhook match will miss")
	}
	return res.RedirectURL, nil
}

// orderTotal returns the amount still owed on a checkout order (unpaid invoice
// sources + ad-hoc lines) and a Vipps payment description.
func (h *InvoiceHandler) orderTotal(ctx context.Context, orderID string) (float64, string, error) {
	var total float64
	var parts []string

	rows, err := h.db.Query(ctx,
		`SELECT i.invoice_number, i.total_amount, i.status, i.payment_id, i.vipps_paid_at
		   FROM order_sources os
		   JOIN invoices i ON i.id = os.invoice_id
		  WHERE os.order_id = $1
		  ORDER BY i.invoice_number`,
		orderID,
	)
	if err != nil {
		return 0, "", err
	}
	for rows.Next() {
		var (
			number    int
			amount    float64
			status    string
			paymentID *string
			paidAt    *string
		)
		if err := rows.Scan(&number, &amount, &status, &paymentID, &paidAt); err != nil {
			rows.Close()
			return 0, "", err
		}
		// Skip anything already settled: voided, reconciled-paid, or received via Vipps.
		if status == "voided" || paymentID != nil || paidAt != nil {
			continue
		}
		total += amount
		parts = append(parts, fmt.Sprintf("Faktura #%d", number))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, "", err
	}
	rows.Close()

	lineRows, err := h.db.Query(ctx,
		`SELECT name, total_price FROM order_lines WHERE order_id = $1`,
		orderID,
	)
	if err != nil {
		return 0, "", err
	}
	defer lineRows.Close()
	for lineRows.Next() {
		var (
			name  string
			price float64
		)
		if err := lineRows.Scan(&name, &price); err != nil {
			return 0, "", err
		}
		total += price
		if name != "" {
			parts = append(parts, name)
		}
	}
	if err := lineRows.Err(); err != nil {
		return 0, "", err
	}

	desc := strings.Join(parts, ", ")
	if desc == "" {
		desc = "Betaling"
	}
	// Vipps caps paymentDescription at 100 chars.
	if len(desc) > 100 {
		desc = desc[:100]
	}
	return total, desc, nil
}

// vippsReturnURL is where Vipps sends the payer's browser after they complete
// (or abort) the payment: the club site.
func (h *InvoiceHandler) vippsReturnURL() string {
	return h.frontendBase()
}

// genVippsReference returns a globally unique, Vipps-safe payment reference
// ([a-zA-Z0-9-], 8–50 chars). Vipps rejects reuse, so each payment is fresh.
func genVippsReference() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "chk-" + hex.EncodeToString(b[:])
}

// genCheckoutToken returns an unguessable token for the public pay link.
func genCheckoutToken() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
