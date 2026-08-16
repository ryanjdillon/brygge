package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// recentPayment is one paid checkout order surfaced to the harbor master.
type recentPayment struct {
	ID      string    `json:"id"`
	PaidAt  time.Time `json:"paid_at"`
	Amount  float64   `json:"amount"`
	Payer   string    `json:"payer"`
	Summary string    `json:"summary"`
	Kind    string    `json:"kind"`    // guest | faktura | merch | other
	Channel string    `json:"channel"` // vipps (later: stripe, …)
}

// HandleRecentPayments returns recent paid orders for the near-realtime harbor
// overview (BRY-265). Fed by the ePayment webhook (orders.status='paid'), so it
// reflects payments within seconds — independent of settlement. Filters:
// since/until (dates), kind, q (payer search), limit.
func (h *HarborHandler) HandleRecentPayments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	limit := 50
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	kindFilter := strings.TrimSpace(q.Get("kind"))
	search := strings.TrimSpace(q.Get("q"))

	var clubID string
	if err := h.db.QueryRow(ctx, `SELECT id FROM clubs WHERE slug = $1`, h.config.ClubSlug).Scan(&clubID); err != nil {
		h.log.Error().Err(err).Msg("recent payments: resolve club failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
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
	if search != "" {
		args = append(args, "%"+search+"%")
		where += fmt.Sprintf(" AND (COALESCE(u.full_name, '') ILIKE $%d OR o.guest_name ILIKE $%d)", len(args), len(args))
	}

	sql := `
		SELECT o.id, o.paid_at, o.total_amount, COALESCE(NULLIF(o.provider, ''), 'vipps'),
		       COALESCE(NULLIF(u.full_name, ''), NULLIF(o.guest_name, ''), ''),
		       (SELECT string_agg('Faktura #' || i.invoice_number, ', ')
		          FROM order_sources os JOIN invoices i ON i.id = os.invoice_id
		         WHERE os.order_id = o.id),
		       (SELECT string_agg(ol.name, ', ')
		          FROM order_lines ol WHERE ol.order_id = o.id),
		       (SELECT string_agg(DISTINCT pi.category, ',')
		          FROM order_lines ol JOIN price_items pi ON pi.id = ol.price_item_id
		         WHERE ol.order_id = o.id),
		       EXISTS(SELECT 1 FROM order_sources os WHERE os.order_id = o.id)
		  FROM orders o
		  LEFT JOIN users u ON u.id = o.user_id
		 WHERE ` + where + `
		 ORDER BY o.paid_at DESC NULLS LAST
		 LIMIT ` + strconv.Itoa(limit)

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
			categories     *string
			hasInvoice     bool
		)
		if err := rows.Scan(&p.ID, &paidAt, &p.Amount, &p.Channel, &p.Payer,
			&invoiceSummary, &lineSummary, &categories, &hasInvoice); err != nil {
			h.log.Error().Err(err).Msg("recent payments: scan failed")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		if paidAt != nil {
			p.PaidAt = *paidAt
		}
		p.Kind = derivePaymentKind(hasInvoice, strval(categories))
		if kindFilter != "" && p.Kind != kindFilter {
			continue
		}
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

// derivePaymentKind classifies a paid order for the harbor overview. Guest stays
// take priority (that's what the harbor master watches for), then faktura, then
// merchandise.
func derivePaymentKind(hasInvoice bool, categories string) string {
	if strings.Contains(categories, "guest") { // guest_slip, guest
		return "guest"
	}
	if strings.Contains(categories, "motorhome") || strings.Contains(categories, "bobil") {
		return "motorhome"
	}
	if hasInvoice {
		return "faktura"
	}
	if strings.Contains(categories, "merch") {
		return "merch"
	}
	return "other"
}

func strval(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
