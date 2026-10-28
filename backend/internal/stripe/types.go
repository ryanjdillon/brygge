package stripe

import (
	"bytes"
	"encoding/json"
)

// Checkout Session statuses.
const (
	SessionOpen     = "open"
	SessionComplete = "complete"
	SessionExpired  = "expired"
)

// PaymentIntent statuses Brygge acts on.
const (
	PaymentIntentRequiresCapture = "requires_capture"
	PaymentIntentSucceeded       = "succeeded"
	PaymentIntentCanceled        = "canceled"
)

// CheckoutSession is the part of a Checkout Session Brygge reads.
type CheckoutSession struct {
	ID string `json:"id"`
	// URL is the hosted payment page; set only while the session is open.
	URL string `json:"url"`
	// Status is open, complete or expired.
	Status string `json:"status"`
	// PaymentStatus is paid, unpaid or no_payment_required. With manual
	// capture a completed card session is unpaid: authorized, not taken.
	PaymentStatus   string            `json:"payment_status"`
	AmountTotal     int64             `json:"amount_total"`
	Currency        string            `json:"currency"`
	Metadata        map[string]string `json:"metadata"`
	CustomerDetails *CustomerDetails  `json:"customer_details"`
	PaymentIntent   *PaymentIntentRef `json:"payment_intent"`
}

// CustomerDetails is what Checkout collected from the payer.
type CustomerDetails struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// PaymentIntentRef is a session's payment_intent, which Stripe sends as a
// bare id unless the request expanded it into the object.
type PaymentIntentRef struct {
	ID string
	// Object is set only when the PaymentIntent was expanded.
	Object *PaymentIntent
}

// UnmarshalJSON accepts both the id string and the expanded object.
func (r *PaymentIntentRef) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("null")) {
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &r.ID)
	}
	var pi PaymentIntent
	if err := json.Unmarshal(b, &pi); err != nil {
		return err
	}
	r.ID, r.Object = pi.ID, &pi
	return nil
}

// PaymentIntent is the part of a PaymentIntent Brygge reads.
type PaymentIntent struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Amount int64  `json:"amount"`
	// AmountReceived is what was captured, which a partial capture makes
	// less than Amount.
	AmountReceived int64             `json:"amount_received"`
	Currency       string            `json:"currency"`
	Metadata       map[string]string `json:"metadata"`
}

// Charge is the part of a Charge event object Brygge reads.
type Charge struct {
	ID            string            `json:"id"`
	PaymentIntent string            `json:"payment_intent"`
	Metadata      map[string]string `json:"metadata"`
}

// Event is a webhook event. Data.Object is decoded by the receiver according
// to Type.
type Event struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Created int64  `json:"created"`
	Data    struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

// Event types Brygge handles or records.
const (
	EventCheckoutSessionCompleted             = "checkout.session.completed"
	EventCheckoutSessionAsyncPaymentSucceeded = "checkout.session.async_payment_succeeded"
	EventCheckoutSessionAsyncPaymentFailed    = "checkout.session.async_payment_failed"
	EventCheckoutSessionExpired               = "checkout.session.expired"
	EventPaymentIntentSucceeded               = "payment_intent.succeeded"
	EventPaymentIntentCanceled                = "payment_intent.canceled"
)
