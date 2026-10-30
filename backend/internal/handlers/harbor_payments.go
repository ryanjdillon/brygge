package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/paymentfilters"
	"github.com/brygge-klubb/brygge/internal/vipps"
)

// recentPayment is one paid (or since refunded) checkout order surfaced to the
// harbor master.
type recentPayment struct {
	ID      string    `json:"id"`
	PaidAt  time.Time `json:"paid_at"`
	Amount  float64   `json:"amount"`
	Payer   string    `json:"payer"`
	Summary string    `json:"summary"`
	// Kind is the slug of the filter group the payment is in (BRY-522); the
	// seeded groups keep the old ids (guest, motorhome, slipping, faktura,
	// other).
	Kind    string `json:"kind"`
	Channel string `json:"channel"` // the provider: vipps | stripe
	// Method is how the payer paid: vipps | card | apple_pay | google_pay.
	Method string `json:"method"`
	// CardBrand is Stripe's brand code (visa, mastercard, ...) for a card
	// payment whose charge has been read; null otherwise.
	CardBrand *string `json:"card_brand"`
	// CanRefund is set only for a treasurer or admin, for a payment that has
	// money left to refund through its provider.
	CanRefund bool `json:"can_refund"`
	// RefundState is none, partial or full, from the refunds the provider
	// confirmed against the payment that paid the order, and RefundedAmount
	// what they add up to. They do not wait for the refund to be booked.
	RefundState    string  `json:"refund_state"`
	RefundedAmount float64 `json:"refunded_amount"`
	// DisputeStatus is the provider's word for the latest dispute against
	// the order's payments (Stripe's needs_response, won, lost, ...); null
	// when there is none. DisputeDueBy is the deadline to answer it.
	DisputeStatus *string    `json:"dispute_status"`
	DisputeDueBy  *time.Time `json:"dispute_due_by"`
	// PaymentURL and DisputeURL are Stripe Dashboard links, set only for a
	// treasurer or admin (who can open the Dashboard).
	PaymentURL string `json:"payment_url,omitempty"`
	DisputeURL string `json:"dispute_url,omitempty"`
}

// providerLinks are the provider back offices the overview links to, for the
// providers the club has configured. They are the final URLs, never the keys
// they were derived from.
type providerLinks struct {
	Stripe string `json:"stripe,omitempty"`
	Vipps  string `json:"vipps,omitempty"`
}

// providerLinksFor builds the links from config; a provider that is not fully
// configured gets none.
func providerLinksFor(cfg *config.Config) providerLinks {
	var l providerLinks
	if complete, _ := cfg.StripeCredentials(); complete {
		l.Stripe = stripeDashboardURL(cfg, "/payments")
	}
	l.Vipps = cfg.VippsPortalURL()
	return l
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

// recentPaymentsQuery builds the overview query from the request filters:
// since/until (dates, both inclusive of their whole day), kind (one group slug
// or a comma-separated list; the group is decided in SQL by
// paymentfilters.OrderGroupLateralSQL, so the filter applies before LIMIT),
// q (payer search) and limit. It also returns the
// limit it applied: the query fetches one row more, so the caller can tell the
// list was cut short.
func recentPaymentsQuery(clubID string, q url.Values) (string, []any, int) {
	limit := defaultRecentLimit
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= maxRecentLimit {
		limit = v
	}

	args := []any{clubID}
	// A refunded order stays in the overview, so the treasurer sees where the
	// money went back; its paid_at is the capture time.
	where := "o.club_id = $1 AND o.status IN ('paid', 'refunded')"
	if since := q.Get("since"); since != "" {
		args = append(args, since)
		where += fmt.Sprintf(" AND o.paid_at >= $%d", len(args))
	}
	if until := q.Get("until"); until != "" {
		// until names the last day to include, so the bound is the start of the
		// day after; a value that is not a date is bound as given.
		args = append(args, inclusiveUntil(until))
		where += fmt.Sprintf(" AND o.paid_at < $%d", len(args))
	}
	if search := strings.TrimSpace(q.Get("q")); search != "" {
		args = append(args, "%"+search+"%")
		where += fmt.Sprintf(" AND (COALESCE(u.full_name, '') ILIKE $%d OR o.guest_name ILIKE $%d)", len(args), len(args))
	}
	if kinds := splitList(q.Get("kind")); len(kinds) > 0 {
		args = append(args, kinds)
		where += fmt.Sprintf(" AND COALESCE(pfg.slug, 'other') = ANY($%d::text[])", len(args))
	}

	sql := `
		SELECT o.id, o.paid_at, o.total_amount, COALESCE(NULLIF(o.provider, ''), 'vipps'),
		       COALESCE(NULLIF(u.full_name, ''), NULLIF(o.guest_name, ''), ''),
		       (SELECT string_agg('Faktura #' || i.invoice_number, ', ')
		          FROM order_sources os JOIN invoices i ON i.id = os.invoice_id
		         WHERE os.order_id = o.id),
		       (SELECT string_agg(ol.name, ', ')
		          FROM order_lines ol WHERE ol.order_id = o.id),
		       COALESCE(pfg.slug, 'other'), ca.card_brand, ca.card_wallet,
		       o.status = 'refunded', pr.amount_ore, pr.refunded_ore, pr.payment_ref,
		       dp.status, dp.evidence_due_by, dp.provider_dispute_id
		  FROM orders o
		  LEFT JOIN users u ON u.id = o.user_id
		 ` + paymentfilters.OrderGroupLateralSQL + `
		  LEFT JOIN LATERAL (
		       SELECT pa.card_brand, pa.card_wallet
		         FROM payment_attempts pa
		        WHERE pa.order_id = o.id AND pa.status = 'captured'
		          AND pa.provider = COALESCE(NULLIF(o.provider, ''), 'vipps')
		        ORDER BY pa.updated_at DESC
		        LIMIT 1) ca ON true
		  LEFT JOIN LATERAL (
		       -- The payment that paid the order: a later capture is a duplicate,
		       -- refunded on its own, and says nothing about the order.
		       SELECT round(pa.amount * 100)::bigint AS amount_ore,
		              COALESCE((SELECT SUM(round(r.amount * 100)) FROM payment_refunds r
		                         WHERE r.attempt_id = pa.id AND r.status = 'succeeded'), 0)::bigint AS refunded_ore,
		              COALESCE((SELECT r.provider_payment_ref FROM payment_refunds r
		                         WHERE r.attempt_id = pa.id AND r.provider_payment_ref IS NOT NULL LIMIT 1),
		                       (SELECT t.payment_intent_id FROM stripe_balance_transactions t
		                         WHERE t.club_id = pa.club_id AND t.attempt_id = pa.id
		                           AND t.reporting_category = 'charge' AND t.payment_intent_id <> '' LIMIT 1), '') AS payment_ref
		         FROM payment_attempts pa
		        WHERE pa.order_id = o.id AND pa.status = 'captured'
		        ORDER BY pa.created_at, pa.id
		        LIMIT 1) pr ON true
		  LEFT JOIN LATERAL (
		       SELECT d.status, d.evidence_due_by, d.provider_dispute_id
		         FROM payment_disputes d
		        WHERE d.club_id = o.club_id
		          AND (d.order_id = o.id
		               OR d.attempt_id IN (SELECT a.id FROM payment_attempts a WHERE a.order_id = o.id))
		        ORDER BY d.created_at DESC, d.id
		        LIMIT 1) dp ON true
		 WHERE ` + where + `
		 ORDER BY o.paid_at DESC NULLS LAST
		 LIMIT ` + strconv.Itoa(limit+1)
	return sql, args, limit
}

const (
	defaultRecentLimit = 50
	maxRecentLimit     = 200
)

// inclusiveUntil turns the last day to include (YYYY-MM-DD) into the exclusive
// upper bound: the next day. Like since, the date is read by the database in
// its session time zone.
func inclusiveUntil(until string) string {
	d, err := time.Parse("2006-01-02", until)
	if err != nil {
		return until
	}
	return d.AddDate(0, 0, 1).Format("2006-01-02")
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

	claims := middleware.GetClaims(ctx)
	staff := claims != nil && mayRefund(claims.Roles)
	sql, args, limit := recentPaymentsQuery(clubID, r.URL.Query())
	rows, err := h.db.Query(ctx, sql, args...)
	if err != nil {
		h.log.Error().Err(err).Msg("recent payments: query failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	out := make([]recentPayment, 0)
	hasMore := false
	for rows.Next() {
		if len(out) == limit {
			// The one extra row the query fetched: there is more than was asked for.
			hasMore = true
			break
		}
		var (
			p              recentPayment
			paidAt         *time.Time
			invoiceSummary *string
			lineSummary    *string
			cardWallet     *string
			orderRefunded  bool
			paidOre        *int64
			refundedOre    *int64
			paymentRef     *string
			disputeID      *string
		)
		if err := rows.Scan(&p.ID, &paidAt, &p.Amount, &p.Channel, &p.Payer,
			&invoiceSummary, &lineSummary, &p.Kind, &p.CardBrand, &cardWallet,
			&orderRefunded, &paidOre, &refundedOre, &paymentRef,
			&p.DisputeStatus, &p.DisputeDueBy, &disputeID); err != nil {
			h.log.Error().Err(err).Msg("recent payments: scan failed")
			Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		if paidAt != nil {
			p.PaidAt = *paidAt
		}
		p.Method = paymentMethod(p.Channel, cardWallet)
		p.RefundState, p.RefundedAmount = overviewRefund(orderRefunded, p.Amount, paidOre, refundedOre)
		if staff {
			// That a named member charged back is for the treasurer and
			// admin; the harbor master reads this list too.
			p.PaymentURL = stripePaymentDashboardURL(h.config, strval(paymentRef))
			p.DisputeURL = stripeDisputeDashboardURL(h.config, strval(disputeID))
		} else {
			p.DisputeStatus, p.DisputeDueBy = nil, nil
		}
		p.Summary = strings.TrimSpace(strings.Trim(strval(invoiceSummary)+" · "+strval(lineSummary), " ·"))
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		h.log.Error().Err(err).Msg("recent payments: rows err")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.markRefundable(r, clubID, out)
	resp := map[string]any{"payments": out, "has_more": hasMore}
	groups, err := paymentfilters.ListGroups(ctx, h.db, clubID, false)
	if err != nil {
		// Without the groups the page cannot draw its chips, so this is a
		// failed overview, not a partial one.
		h.log.Error().Err(err).Msg("recent payments: filter groups failed")
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp["groups"] = overviewGroups(groups)
	if claims != nil && mayEditPaymentFilters(claims.Roles) {
		// The nudge for those who can assign price items to groups.
		if n, err := paymentfilters.CountUnassigned(ctx, h.db, clubID); err != nil {
			h.log.Warn().Err(err).Msg("recent payments: unassigned count failed")
		} else {
			resp["unassigned_items"] = n
		}
	}
	if staff {
		// The back offices are for the treasurer and admin, like the
		// per-payment Dashboard links above.
		if links := providerLinksFor(h.config); links != (providerLinks{}) {
			resp["provider_links"] = links
		}
		// The disputes waiting for evidence are the overview's reminder to
		// answer them in Stripe, whatever the list is filtered to.
		var waiting int
		if err := h.db.QueryRow(ctx,
			`SELECT count(*) FROM payment_disputes WHERE club_id = $1 AND `+openDisputeStatusSQL, clubID,
		).Scan(&waiting); err != nil {
			h.log.Warn().Err(err).Msg("recent payments: dispute count failed")
		} else if waiting > 0 {
			resp["open_disputes"] = waiting
			resp["open_disputes_url"] = stripeDashboardURL(h.config, disputesNeedingResponsePath)
		}
	}
	JSON(w, http.StatusOK, resp)
}

// overviewGroup is a filter group as the overview's chips need it.
type overviewGroup struct {
	Slug     string  `json:"slug"`
	Name     *string `json:"name"`
	LabelKey *string `json:"label_key"`
	Icon     string  `json:"icon"`
	Builtin  *string `json:"builtin"`
}

func overviewGroups(groups []paymentfilters.Group) []overviewGroup {
	out := make([]overviewGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, overviewGroup{Slug: g.Slug, Name: g.Name, LabelKey: g.LabelKey, Icon: g.Icon, Builtin: g.Builtin})
	}
	return out
}

// overviewRefund says how much of an order's payment went back, in kr. The
// provider's confirmed refunds decide; an order already marked refunded (the
// booking pass saw the whole payment returned) counts as fully refunded even
// when the refund rows are missing.
func overviewRefund(orderRefunded bool, orderAmount float64, paidOre, refundedOre *int64) (string, float64) {
	var paid, refunded int64
	if paidOre != nil {
		paid = *paidOre
	}
	if refundedOre != nil {
		refunded = *refundedOre
	}
	if orderRefunded && refunded < paid {
		refunded = paid
	}
	if orderRefunded && paid == 0 {
		return refundStateFull, orderAmount
	}
	return refundStateOf(refunded, paid), float64(refunded) / 100
}

// markRefundable sets can_refund on the payments the caller may refund. A
// failed lookup leaves the buttons hidden rather than failing the overview.
func (h *HarborHandler) markRefundable(r *http.Request, clubID string, payments []recentPayment) {
	claims := middleware.GetClaims(r.Context())
	if h.refunds == nil || claims == nil || !mayRefund(claims.Roles) || len(payments) == 0 {
		return
	}
	ids := make([]string, len(payments))
	for i, p := range payments {
		ids[i] = p.ID
	}
	refundable, err := h.refunds.RefundableOrders(r.Context(), clubID, ids)
	if err != nil {
		h.log.Warn().Err(err).Msg("recent payments: refundable lookup failed")
		return
	}
	for i := range payments {
		payments[i].CanRefund = refundable[payments[i].ID]
	}
}

func strval(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
