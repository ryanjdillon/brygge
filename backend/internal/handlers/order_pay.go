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

	"github.com/brygge-klubb/brygge/internal/finance"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/payments"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

// errNothingToPay signals a checkout order whose invoice sources are all already
// settled and which carries no ad-hoc lines — the payer is sent back to the site
// instead of a zero-amount Vipps checkout.
var errNothingToPay = errors.New("order has nothing left to pay")

// errVippsBlocked signals a checkout order whose unpaid invoice source is not
// allowed to be paid via Vipps (BRY-283): either over the club's amount cap with
// no override, or explicitly forced off. The payer is steered to bank transfer
// (KID) instead of being charged via Vipps, so the club avoids the fee.
var errVippsBlocked = errors.New("order has a source not payable via Vipps")

// ensureFakturaOrderToken returns the durable token of the faktura's checkout
// order, which the email links to. The order (an `orders` row with an
// order_sources link to the invoice) is created lazily the first time a faktura
// should carry a Vipps link (create=true) and reused forever after, so resends
// keep the same link. Returns "" when no payment provider is configured, the
// invoice is over the club's Vipps amount cap (BRY-283), it is an imported
// faktura whose receivable isn't in the GL yet (BRY-386), or nothing should be
// offered.
//
// The ePayment itself is NOT minted here: its redirectUrl is only valid ~10
// minutes, so it is useless in a pay-later email. It is minted on click by
// HandleOrderPayRedirect. This only ensures the durable order + link exist.
func (h *InvoiceHandler) ensureFakturaOrderToken(ctx context.Context, clubID, invoiceID string, create bool) string {
	if !h.providers.Configured() {
		return ""
	}

	// Respect the club's Vipps amount cap: large invoices are bank-transfer only.
	// Checked before the reuse SELECT below so an invoice that already has a
	// durable order stops offering Vipps once the club sets a cap under it.
	if !h.invoiceVippsAllowed(ctx, clubID, invoiceID) {
		return ""
	}
	// Likewise before the reuse: the Vipps charge would credit 1500 for a
	// receivable Brygge never booked. The member still owes it and pays by
	// bank transfer, which waits in Tildel until the opening balance exists.
	if !h.invoiceReceivableInGL(ctx, clubID, invoiceID) {
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

// vippsAllowedExpr is the tri-state rule deciding whether an invoice may offer a
// Vipps link (BRY-283), given aliases i (invoices) and c (clubs):
//
//	Nothing owed (total ≤ 0: a credit note, or a fully exempt faktura) → never
//	On   (opt_in TRUE)  → always
//	Off  (opt_in FALSE) → never
//	Auto (opt_in NULL)  → iff the club has no cap or the amount is within it
const vippsAllowedExpr = `CASE
	WHEN i.total_amount <= 0                  THEN false
	WHEN i.vipps_payment_link_opt_in IS TRUE  THEN true
	WHEN i.vipps_payment_link_opt_in IS FALSE THEN false
	WHEN c.vipps_max_amount IS NULL           THEN true
	ELSE i.total_amount <= c.vipps_max_amount
END`

// invoiceVippsAllowed reports whether a Vipps link should be offered for this
// invoice under the tri-state rule above. On error it fails open (returns true)
// rather than silently suppressing a legitimate link.
func (h *InvoiceHandler) invoiceVippsAllowed(ctx context.Context, clubID, invoiceID string) bool {
	var allowed bool
	err := h.db.QueryRow(ctx,
		`SELECT `+vippsAllowedExpr+`
		   FROM invoices i
		   JOIN clubs c ON c.id = i.club_id
		  WHERE i.id = $1 AND i.club_id = $2`,
		invoiceID, clubID,
	).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) {
		// Unknown invoice — don't suppress here; the create path handles it.
		return true
	}
	if err != nil {
		h.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("vipps allowed check failed")
		return true
	}
	return allowed
}

// invoiceReceivableInGL reports whether a payment today may credit 1500 for
// the invoice (finance.ImportedReceivableOpenSQL): false for an imported
// faktura with no opening balance or invoice sync carrying its receivable
// (BRY-386). Errors fail open for a Brygge faktura and closed for an
// imported one.
func (h *InvoiceHandler) invoiceReceivableInGL(ctx context.Context, clubID, invoiceID string) bool {
	// Only an imported faktura can lack its receivable, so a Brygge faktura
	// never pays for a failed check: a transient error must not drop the
	// Vipps link from an email that can't be sent again.
	var imported bool
	err := h.db.QueryRow(ctx,
		`SELECT import_source IS NOT NULL FROM invoices WHERE id = $1 AND club_id = $2`,
		invoiceID, clubID,
	).Scan(&imported)
	if errors.Is(err, pgx.ErrNoRows) {
		// Unknown invoice: the create path refuses it.
		return true
	}
	if err != nil {
		h.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("imported receivable check: load invoice failed")
		return true
	}
	if !imported {
		return true
	}
	// For an imported faktura a failed check blocks: a missing Vipps link
	// still leaves bank transfer, a wrong one books a receivable never there.
	var ok bool
	if err := h.db.QueryRow(ctx,
		`SELECT `+finance.ImportedReceivableOpenSQL("i", "CURRENT_DATE")+`
		   FROM invoices i
		  WHERE i.id = $1 AND i.club_id = $2`,
		invoiceID, clubID,
	).Scan(&ok); err != nil {
		h.log.Error().Err(err).Str("invoice_id", invoiceID).Msg("imported receivable check failed")
		return false
	}
	return ok
}

// orderHasVippsBlockedSource reports whether any still-unpaid invoice source of
// the order is not allowed to be paid via Vipps (BRY-283), or is an imported
// faktura whose receivable isn't in the GL yet (BRY-386). Used at mint time to
// refuse charging a link that was issued before the cap/override was set (or is
// already sitting in a member's inbox).
func (h *InvoiceHandler) orderHasVippsBlockedSource(ctx context.Context, orderID string) (bool, error) {
	var blocked bool
	err := h.db.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1
		     FROM order_sources os
		     JOIN invoices i ON i.id = os.invoice_id
		     JOIN orders o   ON o.id = os.order_id
		     JOIN clubs c    ON c.id = o.club_id
		    WHERE os.order_id = $1
		      AND `+finance.OwedByMemberSQL("i")+`
		      AND (NOT (`+vippsAllowedExpr+`)
		           OR NOT `+finance.ImportedReceivableOpenSQL("i", "CURRENT_DATE")+`)
		 )`,
		orderID,
	).Scan(&blocked)
	return blocked, err
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
// when Vipps isn't configured so the UI can hide the affordance gracefully. An
// imported faktura without its receivable in the GL is 409 with code
// imported_no_opening_balance (BRY-386).
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
	if !h.providers.Configured() {
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
	if !h.invoiceReceivableInGL(ctx, claims.ClubID, invoiceID) {
		importedNoOpeningBalanceConflict(w)
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
		http.Redirect(w, r, h.paymentReturnURL(), http.StatusFound)
		return
	}
	if !h.providers.Configured() {
		http.Error(w, "Vipps-betaling er ikke tilgjengelig akkurat nå.", http.StatusServiceUnavailable)
		return
	}

	redirectURL, err := h.mintOrderPayment(ctx, vipps.ProviderName, orderID)
	if errors.Is(err, errNothingToPay) {
		http.Redirect(w, r, h.paymentReturnURL(), http.StatusFound)
		return
	}
	if errors.Is(err, errVippsBlocked) {
		// Not payable via Vipps → send them to the checkout page, which shows
		// KID + konto for bank transfer instead of a Vipps button (BRY-283).
		http.Redirect(w, r, h.frontendBase()+"/betal/"+token, http.StatusFound) // #nosec G710 -- fixed configured origin; token is a path segment that just matched a server-minted orders.token
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
// lines, has the named provider create a payment for the total, and stores its
// reference so the provider's webhook can match it back. Returns the
// short-lived checkout URL, or errNothingToPay when nothing is outstanding.
func (h *InvoiceHandler) mintOrderPayment(ctx context.Context, providerName, orderID string) (string, error) {
	provider, ok := h.providers.Get(providerName)
	if !ok {
		return "", fmt.Errorf("payment provider %q is not configured", providerName)
	}

	// Refuse to charge online when an unpaid source isn't allowed (over the
	// club's cap with no override, or forced off) — closes the gap that a token
	// issued before the cap/override (or already emailed) would otherwise leave
	// open (BRY-283).
	blocked, err := h.orderHasVippsBlockedSource(ctx, orderID)
	if err != nil {
		return "", err
	}
	if blocked {
		return "", errVippsBlocked
	}

	total, desc, err := h.orderTotal(ctx, orderID)
	if err != nil {
		return "", err
	}
	if total <= 0 {
		return "", errNothingToPay
	}

	// A guest checkout (QR / walk-up, no linked member) lets the provider ask
	// who paid, so the harbor overview shows a name instead of "Gjest"
	// (BRY-272). Members paying their own faktura are already known.
	var userID *string
	isGuest := false
	if err := h.db.QueryRow(ctx, `SELECT user_id FROM orders WHERE id = $1`, orderID).Scan(&userID); err == nil && userID == nil {
		isGuest = true
	}

	res, err := provider.CreatePayment(ctx, payments.Request{
		OrderID:     orderID,
		AmountOre:   int64(math.Round(total * 100)),
		Currency:    "NOK",
		Description: desc,
		IsGuest:     isGuest,
		ReturnURL:   h.paymentReturnURL(),
	})
	if err != nil {
		return "", err
	}
	if _, err := h.db.Exec(ctx,
		`UPDATE orders SET vipps_reference = $1, total_amount = $2, updated_at = now() WHERE id = $3`,
		res.Reference, total, orderID,
	); err != nil {
		// Payment exists at the provider but the reference didn't persist — the
		// webhook won't match it. Surface loudly but still redirect the payer.
		h.log.Error().Err(err).Str("order_id", orderID).Str("provider", providerName).Str("reference", res.Reference).
			Msg("order pay: created payment but failed to store reference — webhook match will miss")
	}
	return res.RedirectURL, nil
}

// orderTotal returns the amount still owed on a checkout order (unpaid invoice
// sources + ad-hoc lines) and a payment description for the payer.
func (h *InvoiceHandler) orderTotal(ctx context.Context, orderID string) (float64, string, error) {
	var total float64
	var parts []string

	rows, err := h.db.Query(ctx,
		`SELECT i.invoice_number, i.total_amount, `+finance.OwedByMemberSQL("i")+`
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
			number int
			amount float64
			owed   bool
		)
		if err := rows.Scan(&number, &amount, &owed); err != nil {
			rows.Close()
			return 0, "", err
		}
		if !owed {
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
	return total, desc, nil
}

// paymentReturnURL is where the provider sends the payer's browser after they
// complete (or abort) the payment: the club site.
func (h *InvoiceHandler) paymentReturnURL() string {
	return h.frontendBase()
}

// genCheckoutToken returns an unguessable token for the public pay link.
func genCheckoutToken() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
