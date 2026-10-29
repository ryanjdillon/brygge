package stripe

import (
	"encoding/json"
	"strings"
)

// IsDisputeEvent reports whether an event type is one of the charge.dispute
// events, whose object is a Dispute.
func IsDisputeEvent(eventType string) bool {
	switch eventType {
	case EventChargeDisputeCreated, EventChargeDisputeUpdated, EventChargeDisputeClosed,
		EventChargeDisputeFundsWithdrawn, EventChargeDisputeFundsReinstated:
		return true
	}
	return false
}

// Dispute is the part of a Dispute object Brygge reads, from a webhook
// event. The payload is not trusted to have any particular shape, so
// decoding keeps every field that decodes and leaves the rest empty; ID is
// empty when there is no usable object at all.
type Dispute struct {
	ID string
	// PaymentIntent and Charge are the ids of the payment the dispute is
	// about, whether Stripe sent them bare or expanded.
	PaymentIntent string
	Charge        string
	// Amount is the disputed amount in minor units; nil when absent.
	Amount   *int64
	Currency string
	// Reason is Stripe's code for why the cardholder disputed (fraudulent,
	// product_not_received, ...).
	Reason string
	// Status is where the dispute stands (needs_response, under_review,
	// won, lost, ...).
	Status string
	// EvidenceDueBy is the deadline for submitting evidence, unix seconds;
	// 0 when absent.
	EvidenceDueBy int64
	// ChargeRefundable is Stripe's is_charge_refundable: whether the payment
	// can still be refunded; nil when absent.
	ChargeRefundable *bool
}

// UnmarshalJSON decodes best-effort and never fails.
func (d *Dispute) UnmarshalJSON(b []byte) error {
	*d = Dispute{}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil
	}
	field := func(key string, out any) bool {
		v, ok := raw[key]
		return ok && json.Unmarshal(v, out) == nil
	}
	field("id", &d.ID)
	var pi, charge ObjectID
	if field("payment_intent", &pi) {
		d.PaymentIntent = string(pi)
	}
	if field("charge", &charge) {
		d.Charge = string(charge)
	}
	var amount int64
	if field("amount", &amount) {
		d.Amount = &amount
	}
	field("currency", &d.Currency)
	field("reason", &d.Reason)
	field("status", &d.Status)
	var refundable bool
	if field("is_charge_refundable", &refundable) {
		d.ChargeRefundable = &refundable
	}
	var evidence struct {
		DueBy int64 `json:"due_by"`
	}
	if field("evidence_details", &evidence) {
		d.EvidenceDueBy = evidence.DueBy
	}
	return nil
}

// ValidDisputeID reports whether id looks like a Stripe dispute id (dp_…, or
// the older du_…), safe to put in a Dashboard link.
func ValidDisputeID(id string) bool {
	if !strings.HasPrefix(id, "dp_") && !strings.HasPrefix(id, "du_") {
		return false
	}
	for _, r := range id {
		if r != '_' && (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return len(id) > 3
}

// dashboardOrigin is where Stripe's Dashboard lives.
const dashboardOrigin = "https://dashboard.stripe.com"

// DashboardURL is a link to a page of the Stripe Dashboard, for path such as
// "/disputes/dp_123". A test-mode key links to the Dashboard's test mode
// (/test after the domain); a live key links to live mode. The link never
// names the account (no acct_ id): the Dashboard opens the account the
// signed-in user last used.
func DashboardURL(secretKey, path string) string {
	path = "/" + strings.TrimLeft(path, "/")
	if IsTestKey(secretKey) {
		return dashboardOrigin + "/test" + path
	}
	return dashboardOrigin + path
}

// IsTestKey reports whether a Stripe API key is a test-mode key (sk_test_,
// rk_test_).
func IsTestKey(secretKey string) bool {
	return strings.HasPrefix(secretKey, "sk_test_") || strings.HasPrefix(secretKey, "rk_test_")
}
