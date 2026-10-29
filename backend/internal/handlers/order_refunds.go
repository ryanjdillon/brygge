package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/brygge-klubb/brygge/internal/audit"
	"github.com/brygge-klubb/brygge/internal/config"
	"github.com/brygge-klubb/brygge/internal/middleware"
	"github.com/brygge-klubb/brygge/internal/payments"
	"github.com/brygge-klubb/brygge/internal/stripe"
)

// OrderRefundHandler is the refund button's API: start a refund of a paid
// order's card payment and read an order's refunds. It books nothing; the
// Stripe booking pass books a refund, issues its credit note and updates the
// order and faktura once Stripe's balance lists it (ACC-5).
type OrderRefundHandler struct {
	refunds *payments.RefundService
	cfg     *config.Config
	audit   *audit.Service
	log     zerolog.Logger
}

// NewOrderRefundHandler returns the handler over the refund service.
func NewOrderRefundHandler(refunds *payments.RefundService, cfg *config.Config, auditSvc *audit.Service, log zerolog.Logger) *OrderRefundHandler {
	return &OrderRefundHandler{refunds: refunds, cfg: cfg, audit: auditSvc, log: log.With().Str("handler", "order_refunds").Logger()}
}

// refundableOrders answers which orders can be refunded now; the lists that
// show a refund button ask it for their page of rows.
type refundableOrders interface {
	RefundableOrders(ctx context.Context, clubID string, orderIDs []string) (map[string]bool, error)
}

// mayRefund reports whether the roles may start a refund (the roles the
// refund endpoint accepts).
func mayRefund(roles []string) bool {
	return slices.Contains(roles, "treasurer") || slices.Contains(roles, "admin")
}

// Mount registers the refund routes on /orders: reading an order's refunds
// takes the role gate, starting a refund also the step-up gate.
func (h *OrderRefundHandler) Mount(r chi.Router, roleGate, stepUp func(http.Handler) http.Handler) {
	r.Route("/orders", func(r chi.Router) {
		r.Use(roleGate)
		r.Get("/{orderID}/refunds", h.HandleListOrderRefunds)
		r.With(stepUp).Post("/{orderID}/refund", h.HandleRefundOrder)
	})
}

type refundOrderRequest struct {
	AttemptID string `json:"attempt_id"`
	// Amount is in kr; omitted means all that is refundable.
	Amount     *float64 `json:"amount"`
	Reason     string   `json:"reason"`
	Outcome    string   `json:"outcome"`
	RequestKey string   `json:"request_key"`
}

type refundOrderResponse struct {
	Refund   payments.RefundRecord `json:"refund"`
	Replayed bool                  `json:"replayed"`
	// AwaitingConfirmation is a refund Stripe has not been heard to accept:
	// the call's outcome is unknown and is looked up again later.
	AwaitingConfirmation bool `json:"awaiting_confirmation"`
}

// HandleRefundOrder is POST /admin/orders/{orderID}/refund. Treasurer or
// admin with a fresh TOTP; audited order.refund_requested. The request is
// stored before Stripe is called and is idempotent on request_key:
//   - 200 the refund (new, or the same click repeated); status says whether
//     Stripe finished it
//   - 202 Stripe's answer is not known; the refund is looked up again
//   - 409 refused, with a code (payments.Refund* codes)
//   - 502 Stripe refused the refund (code refund_failed, reason Stripe's code)
func (h *OrderRefundHandler) HandleRefundOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	orderID := chi.URLParam(r, "orderID")
	var req refundOrderRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	amountOre, ok := refundAmountOre(req.Amount)
	if !ok {
		Error(w, http.StatusBadRequest, "amount must be a positive amount with at most two decimals")
		return
	}

	out, err := h.refunds.Request(ctx, payments.RefundInput{
		ClubID: claims.ClubID, OrderID: orderID, AttemptID: req.AttemptID, AmountOre: amountOre,
		Reason: req.Reason, Outcome: req.Outcome, RequestKey: req.RequestKey, ActorID: claims.UserID,
	})
	if h.refundFailed(w, err, orderID) {
		return
	}
	if !out.Replayed && h.audit != nil {
		h.audit.LogAction(ctx, claims.ClubID, claims.UserID, r.RemoteAddr,
			audit.ActionOrderRefundRequested, "order", orderID,
			map[string]any{
				"refund_id":   out.Refund.ID,
				"attempt_id":  out.Refund.AttemptID,
				"amount_ore":  out.Refund.AmountOre,
				"outcome":     out.Refund.Outcome,
				"request_key": out.Refund.RequestKey,
				"status":      out.Refund.Status,
				"reason":      out.Refund.Reason,
			})
	}
	if out.Refund.Status == payments.RefundFailed {
		h.writeRejected(w, out)
		return
	}
	body := refundOrderResponse{Refund: out.Refund, Replayed: out.Replayed}
	status := http.StatusOK
	if out.Awaiting && out.Refund.ProviderRefundID == nil {
		body.AwaitingConfirmation = true
		status = http.StatusAccepted
	}
	JSON(w, status, body)
}

// refundAmountOre converts the request's kr to øre. Absent means "all that is
// refundable" (0); a non-positive amount, or one with sub-øre digits, is not
// accepted.
func refundAmountOre(kr *float64) (int64, bool) {
	if kr == nil {
		return 0, true
	}
	ore := math.Round(*kr * 100)
	if ore <= 0 || math.Abs(*kr*100-ore) > 1e-6 || ore > math.MaxInt32*100 {
		return 0, false
	}
	return int64(ore), true
}

func (h *OrderRefundHandler) writeRejected(w http.ResponseWriter, out *payments.RefundResponse) {
	reason := ""
	if out.Refund.FailureReason != nil {
		reason = *out.Refund.FailureReason
	}
	JSON(w, http.StatusBadGateway, map[string]any{
		"error":  "The payment provider did not make the refund",
		"code":   "refund_failed",
		"reason": reason,
		"refund": out.Refund,
	})
}

// refundFailed answers a refund the service refused or could not start, and
// reports whether it did.
func (h *OrderRefundHandler) refundFailed(w http.ResponseWriter, err error, orderID string) bool {
	var refusal *payments.RefundRefusal
	switch {
	case err == nil:
		return false
	case errors.As(err, &refusal):
		JSON(w, http.StatusConflict, map[string]string{"error": refusal.Message, "code": refusal.Code})
	case errors.Is(err, payments.ErrRefundNotFound):
		Error(w, http.StatusNotFound, err.Error())
	case errors.Is(err, payments.ErrRefundInvalid):
		Error(w, http.StatusBadRequest, err.Error())
	default:
		h.log.Error().Err(err).Str("order_id", orderID).Msg("refund request failed")
		Error(w, http.StatusInternalServerError, "internal error")
	}
	return true
}

// HandleListOrderRefunds is GET /admin/orders/{orderID}/refunds: the order's
// captured payments with what is left to refund on each, its refunds and
// disputes, whether a refund (and a recollect) is possible now, and the link
// to the payment in the Stripe Dashboard. Requested refunds Stripe has not
// confirmed for ten minutes are looked up first.
func (h *OrderRefundHandler) HandleListOrderRefunds(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	if claims == nil {
		Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	orderID := chi.URLParam(r, "orderID")
	if _, err := h.refunds.SweepOrder(ctx, claims.ClubID, orderID); err != nil {
		h.log.Warn().Err(err).Str("order_id", orderID).Msg("sweeping the order's refunds failed")
	}
	view, err := h.refunds.OrderRefundView(ctx, claims.ClubID, orderID, h.paymentURL)
	if h.refundFailed(w, err, orderID) {
		return
	}
	JSON(w, http.StatusOK, view)
}

// paymentURL links a Stripe payment to its Dashboard page; empty for another
// provider or an id that is not a PaymentIntent's.
func (h *OrderRefundHandler) paymentURL(provider, paymentRef string) string {
	if provider != stripe.ProviderName || !stripe.ValidPaymentIntentID(paymentRef) {
		return ""
	}
	return stripeDashboardURL(h.cfg, "/payments/"+paymentRef)
}
