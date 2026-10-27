package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Public, token-addressed harbor QR checkout (BRY-6). A poster's QR encodes
// /qr/{token}; the payer picks nights/units and pays. The token is the
// capability — no auth. Amount and revenue category come from the target's
// linked price_item, so pricing has one source of truth.

// qrTarget is a payment target joined to its price_item.
type qrTarget struct {
	ID          string
	ClubID      string
	Label       string
	Location    string
	Kind        string
	PriceItemID string
	ItemName    string
	UnitLabel   string
	UnitAmount  float64
	Currency    string
	MinUnits    int
	MaxUnits    int
	FixedUnits  bool
	// LabelTranslations maps a UI locale code to the label in that language.
	LabelTranslations map[string]string
}

type qrTargetResp struct {
	Label      string  `json:"label"`
	Location   string  `json:"location"`
	Kind       string  `json:"kind"`
	UnitLabel  string  `json:"unit_label"`
	UnitAmount float64 `json:"unit_amount"`
	Currency   string  `json:"currency"`
	MinUnits   int     `json:"min_units"`
	MaxUnits   int     `json:"max_units"`
	FixedUnits bool    `json:"fixed_units"`
	ClubName   string  `json:"club_name"`
	// LabelTranslations lets the page show the visitor's language; Label is
	// the club-language fallback.
	LabelTranslations map[string]string `json:"label_translations"`
}

// resolveQRUnits clamps a requested unit count to the target's bounds. A
// fixed-price target always resolves to 1. Returns the effective unit count and
// whether the request was acceptable (a requested count outside [min,max] is
// rejected rather than silently clamped, so the payer sees the real price).
func resolveQRUnits(minUnits, maxUnits int, fixed bool, requested int) (int, bool) {
	if fixed {
		return 1, true
	}
	if minUnits < 1 {
		minUnits = 1
	}
	if maxUnits < minUnits {
		maxUnits = minUnits
	}
	if requested < minUnits || requested > maxUnits {
		return 0, false
	}
	return requested, true
}

// loadQRTarget resolves a public token to an active target + its price_item.
func (h *InvoiceHandler) loadQRTarget(ctx context.Context, token string) (qrTarget, bool) {
	var t qrTarget
	if token == "" {
		return t, false
	}
	err := h.db.QueryRow(ctx,
		`SELECT t.id, t.club_id, t.label, t.location, t.kind, t.price_item_id,
		        pi.name, t.unit_label, pi.amount, pi.currency,
		        t.min_units, t.max_units, t.fixed_units, t.label_translations
		   FROM harbor_payment_targets t
		   JOIN price_items pi ON pi.id = t.price_item_id
		  WHERE t.token = $1 AND t.archived_at IS NULL AND pi.is_active`,
		token,
	).Scan(&t.ID, &t.ClubID, &t.Label, &t.Location, &t.Kind, &t.PriceItemID,
		&t.ItemName, &t.UnitLabel, &t.UnitAmount, &t.Currency,
		&t.MinUnits, &t.MaxUnits, &t.FixedUnits, &t.LabelTranslations)
	if err != nil {
		return t, false
	}
	return t, true
}

// HandleGetQRTarget returns the public checkout state for a poster's QR token.
func (h *InvoiceHandler) HandleGetQRTarget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t, ok := h.loadQRTarget(ctx, chi.URLParam(r, "token"))
	if !ok {
		Error(w, http.StatusNotFound, "payment target not found")
		return
	}
	var clubName string
	_ = h.db.QueryRow(ctx, `SELECT name FROM clubs WHERE id = $1`, t.ClubID).Scan(&clubName)
	JSON(w, http.StatusOK, qrTargetResp{
		Label:             t.Label,
		Location:          t.Location,
		Kind:              t.Kind,
		UnitLabel:         t.UnitLabel,
		UnitAmount:        t.UnitAmount,
		Currency:          t.Currency,
		MinUnits:          t.MinUnits,
		MaxUnits:          t.MaxUnits,
		FixedUnits:        t.FixedUnits,
		ClubName:          clubName,
		LabelTranslations: t.LabelTranslations,
	})
}

type qrPayRequest struct {
	Units     int    `json:"units"`
	GuestName string `json:"guest_name"`
}

// HandlePayQRTarget creates a guest order for the chosen number of units, mints
// a fresh Vipps ePayment, and returns the checkout redirect URL. The CAPTURED
// webhook later flips the order to paid — no order_sources, so no faktura is
// touched; the paid order surfaces in the harbor overview via its price_item
// category.
func (h *InvoiceHandler) HandlePayQRTarget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.vipps == nil {
		Error(w, http.StatusServiceUnavailable, "Vipps-betaling er ikke tilgjengelig akkurat nå.")
		return
	}
	t, ok := h.loadQRTarget(ctx, chi.URLParam(r, "token"))
	if !ok {
		Error(w, http.StatusNotFound, "payment target not found")
		return
	}

	var req qrPayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return
	}
	units, ok := resolveQRUnits(t.MinUnits, t.MaxUnits, t.FixedUnits, req.Units)
	if !ok {
		Error(w, http.StatusBadRequest, "invalid number of units")
		return
	}

	total := t.UnitAmount * float64(units)
	if total <= 0 {
		Error(w, http.StatusBadRequest, "nothing to pay")
		return
	}

	orderToken := genCheckoutToken()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		h.log.Error().Err(err).Str("target_id", t.ID).Msg("qr pay: begin tx failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback(ctx)

	var orderID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO orders (club_id, guest_name, total_amount, currency, provider, token)
		 VALUES ($1, $2, $3, $4, 'vipps', $5) RETURNING id`,
		t.ClubID, req.GuestName, total, t.Currency, orderToken,
	).Scan(&orderID); err != nil {
		h.log.Error().Err(err).Str("target_id", t.ID).Msg("qr pay: insert order failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO order_lines (order_id, price_item_id, name, quantity, unit_price, total_price)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		orderID, t.PriceItemID, t.ItemName, units, t.UnitAmount, total,
	); err != nil {
		h.log.Error().Err(err).Str("target_id", t.ID).Msg("qr pay: insert order line failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		h.log.Error().Err(err).Str("target_id", t.ID).Msg("qr pay: commit failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	redirectURL, err := h.mintOrderPayment(ctx, orderID)
	if errors.Is(err, errNothingToPay) {
		Error(w, http.StatusBadRequest, "nothing to pay")
		return
	}
	if err != nil {
		h.log.Error().Err(err).Str("order_id", orderID).Msg("qr pay: mint payment failed")
		Error(w, http.StatusBadGateway, "Kunne ikke starte Vipps-betaling. Prøv igjen.")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"redirect_url": redirectURL})
}
