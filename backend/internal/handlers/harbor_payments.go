package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/brygge-klubb/brygge/internal/vipps"
)

// recentPayment is one paid checkout order surfaced to the harbor master.
type recentPayment struct {
	ID      string    `json:"id"`
	PaidAt  time.Time `json:"paid_at"`
	Amount  float64   `json:"amount"`
	Payer   string    `json:"payer"`
	Summary string    `json:"summary"`
	Kind    string    `json:"kind"`    // guest | motorhome | faktura | merch | other
	Channel string    `json:"channel"` // the provider: vipps | stripe
	// Method is how the payer paid: vipps | card | apple_pay | google_pay.
	Method string `json:"method"`
	// CardBrand is Stripe's brand code (visa, mastercard, ...) for a card
	// payment whose charge has been read; null otherwise.
	CardBrand *string `json:"card_brand"`
}

// Payment methods the overview distinguishes.
const (
	methodVipps     = "vipps"
	methodCard      = "card"
	methodApplePay  = "apple_pay"
	methodGooglePay = "google_pay"
)

// paymentMethod is how an order was paid, from its provider and, for a card,
// the wallet Stripe reported. Wallets the overview does not single out, and a
// card whose charge has not been read yet, count as a plain card.
func paymentMethod(provider string, wallet *string) string {
	if provider == vipps.ProviderName {
		return methodVipps
	}
	if wallet != nil {
		switch *wallet {
		case methodApplePay, methodGooglePay:
			return *wallet
		}
	}
	return methodCard
}

// recentPaymentKindSQL classifies a paid order for the harbor overview. Guest
// stays take priority (that is what the harbor master watches for), then
// motorhomes, then faktura, then merchandise. It runs in SQL so the kind
// filter applies before LIMIT.
const recentPaymentKindSQL = `
	CASE
	    WHEN EXISTS (SELECT 1 FROM order_lines ol JOIN price_items pi ON pi.id = ol.price_item_id
	                  WHERE ol.order_id = o.id AND pi.category LIKE '%guest%') THEN 'guest'
	    WHEN EXISTS (SELECT 1 FROM order_lines ol JOIN price_items pi ON pi.id = ol.price_item_id
	                  WHERE ol.order_id = o.id AND (pi.category LIKE '%motorhome%' OR pi.category LIKE '%bobil%')) THEN 'motorhome'
	    WHEN EXISTS (SELECT 1 FROM order_sources os WHERE os.order_id = o.id) THEN 'faktura'
	    WHEN EXISTS (SELECT 1 FROM order_lines ol JOIN price_items pi ON pi.id = ol.price_item_id
	                  WHERE ol.order_id = o.id AND pi.category LIKE '%merch%') THEN 'merch'
	    ELSE 'other'
	END`

// recentPaymentsQuery builds the overview query from the request filters:
// since/until (dates), kind (one kind or a comma-separated list), q (payer
// search) and limit.
func recentPaymentsQuery(clubID string, q url.Values) (string, []any) {
	limit := 50
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}

	args := []any{clubID}
	where := "o.club_id = $1 AND o.status = 'paid'"
	if since := q.Get("since"); since != "" {
		args = append(args, since)
		where += fmt.Sprintf(" AND o.paid_at >= $%d", len(args))
	}
	if until := q.Get("until"); until != "" {
		args = append(args, until)
		where += fmt.Sprintf(" AND o.paid_at < $%d", len(args))
	}
	if search := strings.TrimSpace(q.Get("q")); search != "" {
		args = append(args, "%"+search+"%")
		where += fmt.Sprintf(" AND (COALESCE(u.full_name, '') ILIKE $%d OR o.guest_name ILIKE $%d)", len(args), len(args))
	}
	if kinds := splitList(q.Get("kind")); len(kinds) > 0 {
		args = append(args, kinds)
		where += fmt.Sprintf(" AND k.kind = ANY($%d::text[])", len(args))
	}

	sql := `
		SELECT o.id, o.paid_at, o.total_amount, COALESCE(NULLIF(o.provider, ''), 'vipps'),
		       COALESCE(NULLIF(u.full_name, ''), NULLIF(o.guest_name, ''), ''),
		       (SELECT string_agg('Faktura #' || i.invoice_number, ', ')
		          FROM order_sources os JOIN invoices i ON i.id = os.invoice_id
		         WHERE os.order_id = o.id),
		       (SELECT string_agg(ol.name, ', ')
		          FROM order_lines ol WHERE ol.order_id = o.id),
		       k.kind, ca.card_brand, ca.card_wallet
		  FROM orders o
		  LEFT JOIN users u ON u.id = o.user_id
		 CROSS JOIN LATERAL (SELECT ` + recentPaymentKindSQL + ` AS kind) k
		  LEFT JOIN LATERAL (
		       SELECT pa.card_brand, pa.card_wallet
		         FROM payment_attempts pa
		        WHERE pa.order_id = o.id AND pa.status = 'captured'
		          AND pa.provider = COALESCE(NULLIF(o.provider, ''), 'vipps')
		        ORDER BY pa.updated_at DESC
		        LIMIT 1) ca ON true
		 WHERE ` + where + `
		 ORDER BY o.paid_at DESC NULLS LAST
		 LIMIT ` + strconv.Itoa(limit)
	return sql, args
}

// splitList splits a comma-separated query value, dropping blanks.
func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// HandleRecentPayments returns recent paid orders for the near-realtime harbor
// overview (BRY-265). Fed by the providers' webhooks (orders.status='paid'),
// so it reflects payments within seconds — independent of settlement. Each
// payment says how it was paid (Vipps, card, Apple Pay, Google Pay).
func (h *HarborHandler) HandleRecentPayments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var clubID string
	if err := h.db.QueryRow(ctx, `SELECT id FROM clubs WHERE slug = $1`, h.config.ClubSlug).Scan(&clubID); err != nil {
		h.log.Error().Err(err).Msg("recent payments: resolve club failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	sql, args := recentPaymentsQuery(clubID, r.URL.Query())
	rows, err := h.db.Query(ctx, sql, args...)
	if err != nil {
		h.log.Error().Err(err).Msg("recent payments: query failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	out := make([]recentPayment, 0)
	for rows.Next() {
		var (
			p              recentPayment
			paidAt         *time.Time
			invoiceSummary *string
			lineSummary    *string
			cardWallet     *string
		)
		if err := rows.Scan(&p.ID, &paidAt, &p.Amount, &p.Channel, &p.Payer,
			&invoiceSummary, &lineSummary, &p.Kind, &p.CardBrand, &cardWallet); err != nil {
			h.log.Error().Err(err).Msg("recent payments: scan failed")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		if paidAt != nil {
			p.PaidAt = *paidAt
		}
		p.Method = paymentMethod(p.Channel, cardWallet)
		p.Summary = strings.TrimSpace(strings.Trim(strval(invoiceSummary)+" · "+strval(lineSummary), " ·"))
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("recent payments: rows err")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	JSON(w, http.StatusOK, map[string]any{"payments": out})
}

func strval(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
